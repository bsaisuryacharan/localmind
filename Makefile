# localmind: contributor build targets that mirror .github/workflows/release.yml.

VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)
TARGETS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: build dist clean

build:
	cd wizard && go build -trimpath -ldflags="$(LDFLAGS)" -o ../bin/localmind ./cmd/localmind

dist:
	@rm -rf dist build
	@mkdir -p dist
	@cd wizard && for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		bin="localmind"; [ "$$os" = "windows" ] && bin="localmind.exe"; \
		out="../build/$$os-$$arch"; \
		mkdir -p "$$out"; \
		echo "==> $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
		  go build -trimpath -ldflags="$(LDFLAGS)" -o "$$out/$$bin" ./cmd/localmind; \
		if [ "$$os" = "windows" ]; then \
		  ( cd "$$out" && zip -q "../../dist/localmind-windows-$$arch.zip" "$$bin" ); \
		else \
		  tar -czf "../dist/localmind-$$os-$$arch.tar.gz" -C "$$out" "$$bin"; \
		fi; \
	done
	@cd dist && sha256sum localmind-*.tar.gz localmind-*.zip > checksums.txt
	@ls -lh dist

clean:
	rm -rf bin build dist
