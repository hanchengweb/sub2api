"""Private online speech adapter; account authentication stays in WindHub."""
import asyncio
from typing import Literal

import anyio
import edge_tts
from fastapi import FastAPI, HTTPException, Request, Response
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from fastapi.routing import APIRoute
from pydantic import BaseModel, ConfigDict, Field

VOICES = {"warm": "zh-CN-XiaoxiaoNeural", "bright": "zh-CN-YunxiNeural"}
LIMIT = 4 * 1024 * 1024


def valid_mp3(data):
    return len(data) >= 4 and (data[:3] == b"ID3" or data[0] == 255 and data[1] & 224 == 224)


class SpeechRoute(APIRoute):
    def get_route_handler(self):
        handler = super().get_route_handler()

        async def limited(request):
            if request.method == "POST":
                try:
                    length = int(request.headers.get("content-length", "0"))
                except ValueError:
                    length = 0
                if length < 1 or length > 8192 or request.headers.get("transfer-encoding"):
                    return JSONResponse({"error": "invalid_speech_request"}, status_code=413)
            try:
                response = await handler(request)
            except HTTPException as error:
                error.headers = {**(error.headers or {}), "Cache-Control": "no-store"}
                raise
            response.headers["Cache-Control"] = "no-store"
            return response

        # Preserve the ASGI receive channel so HTTP disconnects reach the handler.
        return limited


class SpeechInput(BaseModel):
    model_config = ConfigDict(extra="forbid", str_strip_whitespace=True)
    input: str = Field(min_length=1, max_length=200)
    voice: Literal["warm", "bright"] = "warm"
    response_format: Literal["mp3"] = "mp3"


class Engine:
    async def generate(self, data):
        chunks, size = [], 0
        voice = edge_tts.Communicate(data.input, VOICES[data.voice], connect_timeout=5, receive_timeout=20)
        async for chunk in voice.stream():
            if chunk.get("type") != "audio":
                continue
            audio = chunk["data"]
            size += len(audio)
            if size > LIMIT:
                raise ValueError("audio_limit")
            chunks.append(audio)
        return b"".join(chunks)


def create_app(engine=None, timeout=25):
    engine = engine or Engine()
    slots = asyncio.Semaphore(2)
    app = FastAPI(docs_url=None, redoc_url=None, openapi_url=None)
    app.router.route_class = SpeechRoute

    @app.exception_handler(RequestValidationError)
    async def invalid(request, exc):
        return JSONResponse({"error": "invalid_speech_request"}, status_code=400, headers={"Cache-Control": "no-store"})

    @app.get("/healthz")
    async def health():
        return {"ready": True}

    @app.post("/synthesize")
    async def synthesize(data: SpeechInput, request: Request):
        if slots.locked():
            raise HTTPException(429, "speech_busy", headers={"Retry-After": "3"})
        await slots.acquire()
        audio, failure = None, None

        async def disconnected(scope):
            while not await request.is_disconnected():
                await anyio.sleep(.05)
            scope.cancel()

        try:
            async with anyio.create_task_group() as group:
                group.start_soon(disconnected, group.cancel_scope)
                try:
                    with anyio.fail_after(timeout):
                        audio = await engine.generate(data)
                except TimeoutError:
                    failure = HTTPException(504, "speech_timeout")
                except Exception:
                    failure = HTTPException(503, "speech_unavailable")
                finally:
                    group.cancel_scope.cancel()
        finally:
            slots.release()
        if failure:
            raise failure
        if audio is None:
            return Response(status_code=499)
        if len(audio) > LIMIT or not valid_mp3(audio):
            raise HTTPException(503, "speech_unavailable")
        return Response(audio, media_type="audio/mpeg", headers={"X-Content-Type-Options": "nosniff"})

    return app


app = create_app()
