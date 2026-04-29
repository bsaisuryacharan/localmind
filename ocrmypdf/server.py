"""Tiny Flask wrapper around OCRmyPDF.

Endpoints:

    POST /v1/ocr      body = raw PDF bytes
                      response = text/plain, the extracted text

    GET  /healthz     liveness probe

OCRmyPDF is invoked with --skip-text so pages that already have a text
layer are passed through unchanged (idempotent for mixed PDFs). The
output PDF is then piped through `pdftotext` to recover plain text.
We use temp files (not stdin/stdout) because OCRmyPDF needs to seek.
"""

import os
import subprocess
import tempfile

from flask import Flask, request, Response

app = Flask(__name__)


@app.get("/healthz")
def healthz():
    return "ok\n", 200


@app.post("/v1/ocr")
def ocr():
    pdf_bytes = request.get_data(cache=False)
    if not pdf_bytes:
        return "empty body\n", 400

    with tempfile.TemporaryDirectory() as tmp:
        in_path = os.path.join(tmp, "in.pdf")
        out_path = os.path.join(tmp, "out.pdf")
        txt_path = os.path.join(tmp, "out.txt")
        with open(in_path, "wb") as f:
            f.write(pdf_bytes)

        # --skip-text: leave pages that already have text alone.
        # --quiet:     suppress progress chatter on stderr.
        # --output-type pdf: skip PDF/A conversion to save time.
        try:
            subprocess.run(
                [
                    "ocrmypdf",
                    "--skip-text",
                    "--quiet",
                    "--output-type", "pdf",
                    in_path,
                    out_path,
                ],
                check=True,
                capture_output=True,
            )
        except subprocess.CalledProcessError as e:
            stderr = e.stderr.decode("utf-8", "replace") if e.stderr else ""
            return f"ocrmypdf failed: {stderr}\n", 500

        # pdftotext is part of poppler-utils, already in the base image.
        try:
            subprocess.run(
                ["pdftotext", "-layout", out_path, txt_path],
                check=True,
                capture_output=True,
            )
        except subprocess.CalledProcessError as e:
            stderr = e.stderr.decode("utf-8", "replace") if e.stderr else ""
            return f"pdftotext failed: {stderr}\n", 500

        with open(txt_path, "rb") as f:
            text = f.read()

    return Response(text, mimetype="text/plain; charset=utf-8")


if __name__ == "__main__":
    # Threaded so a slow OCR job doesn't block /healthz. No need for a
    # real WSGI server — this only ever sees one client (the MCP gateway).
    app.run(host="0.0.0.0", port=8000, threaded=True)
