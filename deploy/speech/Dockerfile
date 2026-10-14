FROM docker.m.daocloud.io/library/python:3.12-slim
ENV PIP_DISABLE_PIP_VERSION_CHECK=1 PYTHONUNBUFFERED=1
RUN pip install --no-cache-dir --index-url https://mirrors.aliyun.com/pypi/simple \
    edge-tts==7.3.1 fastapi==0.143.0 uvicorn==0.54.0
WORKDIR /app
COPY server.py /app/server.py
USER 1000:1000
CMD ["uvicorn", "server:app", "--host", "0.0.0.0", "--port", "8093", "--no-access-log", "--limit-concurrency", "16"]
