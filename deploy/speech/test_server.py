import asyncio
import http.client
import json
import socket
import threading
import time
import unittest
from unittest.mock import patch

import uvicorn
from fastapi.testclient import TestClient
from server import create_app, Engine, SpeechInput, LIMIT

MP3 = b'\xff\xfb\x90\x00' + bytes(128)


class FakeEngine:
    def __init__(self):
        self.calls, self.error = [], None

    async def generate(self, data):
        self.calls.append(data.model_dump())
        if self.error:
            raise self.error
        return MP3


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
        self.assertEqual(result.headers['content-type'], 'audio/mpeg')
        self.assertEqual(result.headers['cache-control'], 'no-store')
        self.assertEqual(result.content, MP3)

    def test_rejects_invalid_requests_and_redacts_errors(self):
        for payload in [{}, {'input': ' '}, {'input': '你'*201}, {'input': 'private', 'voice': 'zh-CN-XiaoxiaoNeural'}, {'input': 'private', 'url': 'https://example.org'}]:
            result = self.client.post('/synthesize', json=payload)
            self.assertEqual(result.status_code, 400)
            self.assertNotIn('private', result.text)
        self.assertEqual(self.engine.calls, [])
        self.engine.error = RuntimeError('private provider details')
        result = self.client.post('/synthesize', json={'input': '你好'})
        self.assertEqual(result.status_code, 503)
        self.assertNotIn('private', result.text)
        self.assertEqual(self.client.post('/synthesize', content=b'x'*8193).status_code, 413)
        self.assertEqual(self.client.get('/docs').status_code, 404)

    def test_timeout_and_audio_integrity(self):
        async def slow(data):
            await asyncio.sleep(30)
        self.engine.generate = slow
        with TestClient(create_app(self.engine, timeout=.05)) as client:
            self.assertEqual(client.post('/synthesize', json={'input': '你好'}).status_code, 504)
        for value in [b'<html>provider error</html>', b'ID3'+bytes(LIMIT)]:
            async def invalid(data):
                return value
            self.engine.generate = invalid
            self.assertEqual(self.client.post('/synthesize', json={'input': '你好'}).status_code, 503)

    def test_real_http_disconnect_and_capacity(self):
        active, stopped = set(), threading.Event()
        async def slow(data):
            active.add(data.input)
            try:
                await asyncio.sleep(30)
                return MP3
            finally:
                active.discard(data.input)
                if not active:
                    stopped.set()
        self.engine.generate = slow
        listener = socket.socket()
        listener.bind(('127.0.0.1', 0))
        listener.listen()
        server = uvicorn.Server(uvicorn.Config(create_app(self.engine), log_level='critical', ws='none'))
        thread = threading.Thread(target=server.run, kwargs={'sockets': [listener]}, daemon=True)
        thread.start()
        connections = [http.client.HTTPConnection('127.0.0.1', listener.getsockname()[1], timeout=3) for _ in range(3)]
        try:
            deadline = time.monotonic()+3
            while not server.started and time.monotonic() < deadline:
                time.sleep(.01)
            for i in range(2):
                connections[i].request('POST', '/synthesize', json.dumps({'input': str(i)}), {'Content-Type': 'application/json'})
            deadline = time.monotonic()+3
            while len(active) < 2 and time.monotonic() < deadline:
                time.sleep(.01)
            self.assertEqual(len(active), 2)
            connections[2].request('POST', '/synthesize', json.dumps({'input': 'busy'}), {'Content-Type': 'application/json'})
            self.assertEqual(connections[2].getresponse().status, 429)
            connections[0].close()
            connections[1].close()
            self.assertTrue(stopped.wait(2))
        finally:
            for connection in connections:
                connection.close()
            server.should_exit = True
            thread.join(3)
            listener.close()

    def test_engine_uses_controlled_voice_and_bounds_provider_stream(self):
        class Stream:
            async def stream(self):
                yield {'type': 'audio', 'data': MP3}
                yield {'type': 'WordBoundary', 'text': 'metadata'}
        with patch('server.edge_tts.Communicate', return_value=Stream()) as communicate:
            result = asyncio.run(Engine().generate(SpeechInput(input='你好')))
            self.assertEqual(result, MP3)
            self.assertEqual(communicate.call_args.args, ('你好', 'zh-CN-XiaoxiaoNeural'))


if __name__ == '__main__':
    unittest.main()
