FROM python:3.12-slim
WORKDIR /app
RUN pip install --no-cache-dir 'torch>=2.6,<3' --index-url https://download.pytorch.org/whl/cpu \
    && pip install --no-cache-dir 'sentence-transformers>=5.0,<6'
COPY server.py /app/server.py
ENV HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1 PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
USER 65534:65534
EXPOSE 8008
CMD ["python", "/app/server.py"]
