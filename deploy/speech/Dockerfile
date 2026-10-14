FROM docker.m.daocloud.io/library/python:3.12-slim
ENV PIP_DISABLE_PIP_VERSION_CHECK=1 PYTHONUNBUFFERED=1 OMP_NUM_THREADS=2 MKL_NUM_THREADS=2 \
    HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1 HF_HOME=/tmp/hf
RUN pip install --no-cache-dir --index-url https://mirrors.aliyun.com/pypi/simple \
    "https://download.pytorch.org/whl/cpu/torch-2.10.0%2Bcpu-cp312-cp312-manylinux_2_28_x86_64.whl#sha256=ee40b8a4b4b2cf0670c6fd4f35a7ef23871af956fecb238fbf5da15a72650b1d" \
    "https://download.pytorch.org/whl/cpu/torchaudio-2.10.0%2Bcpu-cp312-cp312-manylinux_2_28_x86_64.whl#sha256=502b19a5bd0ef8fc97adf06c24146c5e2b94bc3c43b768174407c031cd21c3a5"
RUN pip install --no-cache-dir --index-url https://mirrors.aliyun.com/pypi/simple \
    qwen-tts==0.1.1 huggingface_hub==0.36.2 gradio==6.2.0 numpy==2.2.6 fastapi==0.143.0 uvicorn==0.54.0
WORKDIR /app
COPY server.py /app/server.py
USER 1000:1000
CMD ["uvicorn", "server:app", "--host", "0.0.0.0", "--port", "8093", "--no-access-log", "--limit-concurrency", "16"]
