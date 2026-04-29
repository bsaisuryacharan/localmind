# Tiny HTTP wrapper around OCRmyPDF. Accepts a PDF on POST /v1/ocr,
# OCRs it (skipping pages that already have a text layer), and returns
# the extracted plain text.
#
# Why bake our own image rather than pull a community wrapper:
# OCRmyPDF itself is upstream and well-maintained, but the HTTP-wrapper
# variants on Docker Hub are unmaintained and not reliably published.
# A 30-line Flask shim is more durable than a third-party tag.
FROM jbarlow83/ocrmypdf:latest

# The base image bundles ocrmypdf + tesseract + ghostscript + qpdf.
# We just need Flask for the HTTP shim. Pull it in via the system
# package so we don't have to deal with PEP 668's locked-down pip on
# Debian-based images.
USER root
RUN apt-get update \
    && apt-get install -y --no-install-recommends python3-flask \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY server.py /app/server.py

EXPOSE 8000

# The base image's ENTRYPOINT is `ocrmypdf`; override so we run Flask.
ENTRYPOINT []
CMD ["python3", "/app/server.py"]
