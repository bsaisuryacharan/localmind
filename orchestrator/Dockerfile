FROM python:3.12-slim

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

# Minimal OS deps. We need bash for the entrypoint and ca-certificates for outbound HTTPS
# (Ollama is local but MCP gateway calls may go through TLS-terminating proxies).
RUN apt-get update \
    && apt-get install -y --no-install-recommends bash ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy project metadata first so docker layer cache survives source edits.
COPY pyproject.toml README.md ./
COPY localmind_orchestrator ./localmind_orchestrator
COPY skills ./skills
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

RUN chmod +x /usr/local/bin/docker-entrypoint.sh \
    && pip install -e .

# Volume for SQLite-backed graph history (mounted by docker-compose).
RUN mkdir -p /var/lib/localmind
VOLUME ["/var/lib/localmind"]

EXPOSE 7950

ENV LOCALMIND_ORCHESTRATOR_DB=/var/lib/localmind/orchestrator.db \
    OLLAMA_BASE_URL=http://ollama:11434

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["uvicorn", "localmind_orchestrator.server:app", "--host", "0.0.0.0", "--port", "7950"]
