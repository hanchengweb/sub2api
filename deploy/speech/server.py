"""Private inference worker. Public authentication and quotas belong to WindHub."""
import asyncio
import io
import os
import threading
import time
from contextlib import asynccontextmanager

from fastapi import FastAPI, HTTPException, Request, Response
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from fastapi.routing import APIRoute
from pydantic import BaseModel, ConfigDict, Field
from typing import Literal

VOICES = {"warm": "Serena", "bright": "Vivian"}


class SpeechRoute(APIRoute):
    def get_route_handler(self):
        handler = super().get_route_handler()

        async def limited(request):
            try:
                length = int(request.headers.get("content-length", "0"))
            except ValueError:
                return JSONResponse({"error": "invalid_speech_request"}, status_code=400)
            if request.method == "POST" and (length < 1 or length > 8192 or request.headers.get("transfer-encoding")):
                return JSONResponse({"error": "invalid_speech_request"}, status_code=413)
            try:
                response = await handler(request)
            except HTTPException as exc:
                exc.headers = {**(exc.headers or {}), "Cache-Control": "no-store"}
                raise
            response.headers["Cache-Control"] = "no-store"
            return response

        # Keep the raw ASGI receive channel: BaseHTTPMiddleware can hide disconnects.
        return limited


class SpeechInput(BaseModel):
    model_config = ConfigDict(extra="forbid")
    input: str = Field(min_length=1, max_length=200)
    voice: Literal["warm", "bright"] = "warm"
    response_format: Literal["wav"] = "wav"


class Engine:
    def __init__(self):
        self.model = None
        self.lock = threading.Lock()

    def load(self):
        import torch
        from qwen_tts import Qwen3TTSModel
        torch.set_num_threads(int(os.environ.get("SPEECH_CPU_THREADS", "2")))
        torch.set_num_interop_threads(1)
        device = os.environ.get("SPEECH_DEVICE", "cpu")
        dtype = torch.float32
        if device.startswith("cuda"):
            dtype = torch.bfloat16 if torch.cuda.is_bf16_supported() else torch.float16
        self.model = Qwen3TTSModel.from_pretrained(
            os.environ.get("SPEECH_MODEL_PATH", "/models"), device_map=device,
            dtype=dtype,
            attn_implementation="sdpa",
        )

    def generate(self, data, cancelled):
        import soundfile
        import torch
        deadline = time.monotonic() + 45
        hook = None

        def interrupt(module, args):
            if cancelled.is_set() or time.monotonic() > deadline:
                raise TimeoutError()

        try:
            # qwen-tts 0.1.1 drops stopping_criteria before talker.generate().
            # A request-scoped PyTorch hook interrupts at the next decode step
            # and skips waveform decoding. The worker admits only one request.
            hook = self.model.model.talker.register_forward_pre_hook(interrupt)
            with torch.inference_mode():
                waves, rate = self.model.generate_custom_voice(
                    text=data.input.strip(), language="Auto", speaker=VOICES[data.voice],
                    max_new_tokens=720,
                )
            if cancelled.is_set() or time.monotonic() > deadline:
                raise TimeoutError()
            buffer = io.BytesIO()
            soundfile.write(buffer, waves[0], rate, format="WAV", subtype="PCM_16")
            return buffer.getvalue()
        finally:
            if hook is not None:
                hook.remove()
            # The slot stays held until inference actually stops, including disconnects.
            self.lock.release()


def create_app(engine=None):
    engine = engine or Engine()

    @asynccontextmanager
    async def lifespan(app):
        await asyncio.to_thread(engine.load)
        yield

    app = FastAPI(lifespan=lifespan, docs_url=None, redoc_url=None, openapi_url=None)
    app.router.route_class = SpeechRoute

    @app.exception_handler(RequestValidationError)
    async def invalid(request, exc):
        return JSONResponse({"error": "invalid_speech_request"}, status_code=400, headers={"Cache-Control": "no-store"})

    @app.get("/healthz")
    async def health():
        if engine.model is None:
            raise HTTPException(503, "not_ready")
        return {"ready": True}

    @app.post("/synthesize")
    async def synthesize(data: SpeechInput, request: Request):
        if not data.input.strip():
            raise HTTPException(400, "invalid_speech_request")
        if engine.model is None:
            raise HTTPException(503, "not_ready")
        if not engine.lock.acquire(blocking=False):
            raise HTTPException(429, "speech_busy", headers={"Retry-After": "3"})
        cancelled = threading.Event()
        task = asyncio.create_task(asyncio.to_thread(engine.generate, data, cancelled))
        try:
            while not task.done():
                if await request.is_disconnected():
                    cancelled.set()
                await asyncio.sleep(0.1)
            result = await task
            if cancelled.is_set():
                return Response(status_code=499)
            return Response(result, media_type="audio/wav")
        except asyncio.CancelledError:
            cancelled.set()
            # Retrieve worker exceptions without cancelling the underlying thread.
            task.add_done_callback(lambda done: done.exception() if not done.cancelled() else None)
            raise
        except TimeoutError:
            raise HTTPException(504, "speech_timeout") from None
        except Exception:
            raise HTTPException(503, "speech_unavailable") from None

    return app


app = create_app()
