import threading
import unittest
import http.client
import json
import socket
import time
from contextlib import nullcontext
from types import SimpleNamespace
from unittest.mock import patch
import uvicorn
from fastapi.testclient import TestClient
from server import create_app, Engine, SpeechInput


class FakeEngine:
    def __init__(self):
        self.model = None
        self.lock = threading.Lock()
        self.calls = []
        self.error = None

    def load(self):
        self.model = object()

    def generate(self, data, cancelled):
        try:
            self.calls.append(data.model_dump())
            if self.error:
                raise self.error
            return b"RIFF" + bytes(4) + b"WAVE" + bytes(32)
        finally:
            self.lock.release()


class WorkerTest(unittest.TestCase):
    def setUp(self):
        self.engine = FakeEngine()
        self.client = TestClient(create_app(self.engine))
        self.client.__enter__()

    def tearDown(self):
        self.client.__exit__(None, None, None)

    def test_real_route_bytes_and_no_store(self):
        self.assertTrue(self.client.get('/healthz').json()['ready'])
        result = self.client.post('/synthesize', json={'input': '你好'})
        self.assertEqual(result.status_code, 200)
        self.assertEqual(result.headers['content-type'], 'audio/wav')
        self.assertEqual(result.headers['cache-control'], 'no-store')
        self.assertEqual(result.content[:4], b'RIFF')
        self.assertFalse(self.engine.lock.locked())

    def test_input_constraints_do_not_echo_text_or_internal_errors(self):
        for payload in [{}, {'input': ' '}, {'input': '你'*201}, {'input': 'secret text', 'voice': 'Serena'}, {'input': 'secret text', 'path': '/models'}]:
            result = self.client.post('/synthesize', json=payload)
            self.assertEqual(result.status_code, 400)
            self.assertNotIn('secret text', result.text)
        self.assertEqual(self.engine.calls, [])
        self.engine.error = RuntimeError('private path/model/secret')
        result = self.client.post('/synthesize', json={'input': '你好'})
        self.assertEqual(result.status_code, 503)
        self.assertNotIn('private', result.text)
        self.assertFalse(self.engine.lock.locked())

    def test_busy_timeout_and_size_limits(self):
        self.engine.lock.acquire()
        result = self.client.post('/synthesize', json={'input': '你好'})
        self.assertEqual(result.status_code, 429)
        self.assertEqual(result.headers['retry-after'], '3')
        self.engine.lock.release()
        self.engine.error = TimeoutError()
        self.assertEqual(self.client.post('/synthesize', json={'input': '你好'}).status_code, 504)
        self.assertFalse(self.engine.lock.locked())
        self.assertEqual(self.client.post('/synthesize', content=b'x'*8193).status_code, 413)
        self.assertEqual(self.client.get('/docs').status_code, 404)

    def test_disconnected_http_request_cancels_the_inference_worker(self):
        engine = FakeEngine()
        began, stopped = threading.Event(), threading.Event()

        def slow(data, cancelled):
            try:
                began.set()
                if cancelled.wait(3):
                    stopped.set()
                return b'RIFF0000WAVE' + bytes(32)
            finally:
                engine.lock.release()

        engine.generate = slow
        listener = socket.socket()
        listener.bind(('127.0.0.1', 0))
        listener.listen()
        server = uvicorn.Server(uvicorn.Config(create_app(engine), log_level='critical'))
        thread = threading.Thread(target=server.run, kwargs={'sockets': [listener]}, daemon=True)
        thread.start()
        connection = http.client.HTTPConnection('127.0.0.1', listener.getsockname()[1], timeout=3)
        try:
            deadline = time.monotonic() + 3
            while not server.started and time.monotonic() < deadline:
                time.sleep(.01)
            connection.request('POST', '/synthesize', json.dumps({'input': '你好'}), {'Content-Type': 'application/json'})
            self.assertTrue(began.wait(2))
            connection.close()
            self.assertTrue(stopped.wait(2))
            self.assertFalse(engine.lock.locked())
        finally:
            connection.close()
            server.should_exit = True
            thread.join(3)
            listener.close()

    def test_engine_interrupts_decode_and_removes_request_hook(self):
        engine = Engine()
        callbacks = []

        def register(callback):
            callbacks.append(callback)
            return SimpleNamespace(remove=lambda: callbacks.remove(callback))

        def generate(**kwargs):
            # Mirror the pinned model dropping arbitrary generation kwargs.
            for callback in callbacks:
                callback(None, ())
            self.fail('cancelled request reached waveform decoding')

        engine.model = SimpleNamespace(
            model=SimpleNamespace(talker=SimpleNamespace(register_forward_pre_hook=register)),
            generate_custom_voice=generate,
        )
        modules = {'torch': SimpleNamespace(inference_mode=nullcontext), 'soundfile': SimpleNamespace()}
        for cancelled, clock in [(True, [0, 0]), (False, [0, 46])]:
            event = threading.Event()
            if cancelled:
                event.set()
            engine.lock.acquire()
            with patch.dict('sys.modules', modules), patch('server.time.monotonic', side_effect=clock):
                with self.assertRaises(TimeoutError):
                    engine.generate(SpeechInput(input='你好'), event)
            self.assertEqual(callbacks, [])
            self.assertFalse(engine.lock.locked())


if __name__ == '__main__':
    unittest.main()
