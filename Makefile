# Deploy to palmshell via SSH (see deploy/README.md).
STAGING_HOST        ?= palmshell
STAGING_SSH         ?= ssh
STAGING_SCP         ?= scp
STAGING_BIN_DIR     ?= /root/bin
STAGING_SERVICE     ?= txmlconnector-server.service
STAGING_UNIT_PATH   ?= /etc/systemd/system/$(STAGING_SERVICE)
STAGING_WINE        ?= /usr/lib/wine/wine64
STAGING_DLL_VER     ?= 6.47.2.26.4
STAGING_DLL_LOCAL   ?= txmlconnector64-$(STAGING_DLL_VER).dll
STAGING_DLL_PATH    ?= /root/$(STAGING_DLL_LOCAL)
STAGING_LISTEN_ADDR ?= :50051
STAGING_PORT        ?= $(lastword $(subst :, ,$(STAGING_LISTEN_ADDR)))
STAGING_LOG_DIR     ?= $(STAGING_BIN_DIR)/logs
STAGING_BIN         ?= bin/server.exe
STAGING_REMOTE_BIN  ?= $(STAGING_BIN_DIR)/txmlconnector-server.exe
STAGING_UNIT_TMPL   ?= deploy/txmlconnector-server.service

.PHONY: compile test vet server_build server queues_build queues client tgbot \
	staging-precheck staging-prepare deploy-staging deploy-staging-binary \
	deploy-staging-dll deploy-staging-unit deploy-staging-smoke \
	deploy-staging-status deploy-staging-logs deploy-staging-restart rollback-staging
compile: ## Compile the proto file.
	protoc proto/connect.proto --go_out=. --go-grpc_out=. --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative

test:
	go test ./...

vet:
	go vet ./...

server_build:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-s -w" -o bin/server.exe .

## Build and run the Win32 adapter under Wine.
server: server_build
	mkdir -p logs
	wine64 bin/server.exe

queues_build:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-s -w" -o bin/queues.exe ./examples/queues

queues: queues_build
	wine64 bin/queues.exe

client: ## Build and run client.
	mkdir -p bin
	go build -ldflags "-s -w" -o bin/client ./examples/grpc-client
	bin/client

tgbot: ## Build and run telegram bot app.
	mkdir -p bin
	go build -ldflags "-s -w" -o bin/tgbot ./examples/telegram-bot
	bin/tgbot

build:
	docker build --no-cache -t kmlebedev/txmlconnector:local -f docker/Dockerfile.go_build .

exporter_build:
	docker build --no-cache -t kmlebedev/transaq-clickhouse-exporter:local -f docker/Dockerfile.clickhouse-exporter .

exporter_financial_build:
	docker build --no-cache -t kmlebedev/clickhouse-exporter-financial:local -f docker/Dockerfile.clickhouse-exporter-financial .

grafana_build:
	docker build --no-cache -t kmlebedev/grafana-financial:local -f docker/Dockerfile.grafana .

dev: build
	docker-compose -f docker/compose/local-dev-compose.yml -p transaq up

exporter: build exporter_build
	docker-compose -f docker/compose/clickhouse-exporter-compose.yaml -p transaq up

exporter_financial: exporter_financial_build
	docker-compose -f docker/compose/clickhouse-exporter-financial-compose.yaml -p financial up

dev_financial:
	docker-compose -f docker/compose/clickhouse-exporter-financial-compose.yaml -p financial up

## Deploy to palmshell via SSH. See deploy/README.md for paths and overrides.

staging-precheck:
	@$(STAGING_SSH) $(STAGING_HOST) 'test "$$(id -u)" = 0' || { echo "FAIL: $(STAGING_HOST): нужен root"; exit 1; }
	@$(STAGING_SSH) $(STAGING_HOST) 'test -x $(STAGING_WINE)' || { echo "FAIL: на $(STAGING_HOST) нет $(STAGING_WINE)"; exit 1; }
	@$(STAGING_SSH) $(STAGING_HOST) 'command -v systemctl >/dev/null && command -v sha256sum >/dev/null && command -v ss >/dev/null' || { echo "FAIL: на $(STAGING_HOST) нужны systemctl, sha256sum и ss"; exit 1; }
	@echo "OK   $(STAGING_HOST): root, $(STAGING_WINE), systemctl, sha256sum, ss"

staging-prepare: staging-precheck
	@$(STAGING_SSH) $(STAGING_HOST) 'install -d -m 755 $(STAGING_BIN_DIR) $(STAGING_LOG_DIR)'

deploy-staging-binary: staging-prepare server_build
	@set -e; \
	local_sum=$$(shasum -a 256 "$(STAGING_BIN)" | cut -d' ' -f1); \
	remote_sum=$$($(STAGING_SSH) $(STAGING_HOST) 'sha256sum "$(STAGING_REMOTE_BIN)" 2>/dev/null | cut -d" " -f1' || true); \
	if [ "$$local_sum" = "$$remote_sum" ]; then \
		echo "бинарь не изменился ($$local_sum) — копирование пропущено"; \
	else \
		$(STAGING_SSH) $(STAGING_HOST) 'if [ -f "$(STAGING_REMOTE_BIN)" ]; then cp -f "$(STAGING_REMOTE_BIN)" "$(STAGING_REMOTE_BIN).bak"; fi'; \
		$(STAGING_SCP) "$(STAGING_BIN)" "$(STAGING_HOST):$(STAGING_REMOTE_BIN).new"; \
		$(STAGING_SSH) $(STAGING_HOST) 'mv "$(STAGING_REMOTE_BIN).new" "$(STAGING_REMOTE_BIN)" && chmod 755 "$(STAGING_REMOTE_BIN)"'; \
		echo "бинарь обновлён"; \
	fi

deploy-staging-dll: staging-prepare
	@test -f "$(STAGING_DLL_LOCAL)" || { echo "FAIL: нет $(STAGING_DLL_LOCAL) в репозитории"; exit 1; }
	@set -e; \
	local_sum=$$(shasum -a 256 "$(STAGING_DLL_LOCAL)" | cut -d' ' -f1); \
	remote_sum=$$($(STAGING_SSH) $(STAGING_HOST) 'sha256sum "$(STAGING_DLL_PATH)" 2>/dev/null | cut -d" " -f1' || true); \
	if [ "$$local_sum" = "$$remote_sum" ]; then \
		echo "DLL не изменился ($$local_sum) — копирование пропущено"; \
	else \
		$(STAGING_SSH) $(STAGING_HOST) 'if [ -f "$(STAGING_DLL_PATH)" ]; then cp -f "$(STAGING_DLL_PATH)" "$(STAGING_DLL_PATH).bak"; fi'; \
		$(STAGING_SCP) "$(STAGING_DLL_LOCAL)" "$(STAGING_HOST):$(STAGING_DLL_PATH).new"; \
		$(STAGING_SSH) $(STAGING_HOST) 'mv "$(STAGING_DLL_PATH).new" "$(STAGING_DLL_PATH)" && chmod 644 "$(STAGING_DLL_PATH)"'; \
		echo "DLL обновлён"; \
	fi

deploy-staging-unit: staging-prepare
	@test -f "$(STAGING_UNIT_TMPL)" || { echo "FAIL: нет $(STAGING_UNIT_TMPL)"; exit 1; }
	@tmp=$$(mktemp); trap 'rm -f "$$tmp"' EXIT; \
	sed -e 's|__TXML_WINE__|$(STAGING_WINE)|g' \
		-e 's|__TXML_BIN_DIR__|$(STAGING_BIN_DIR)|g' \
		-e 's|__TXML_DLL_PATH__|$(STAGING_DLL_PATH)|g' \
		-e 's|__TXML_LISTEN_ADDR__|$(STAGING_LISTEN_ADDR)|g' \
		-e 's|__TXML_LOG_DIR__|$(STAGING_LOG_DIR)|g' \
		"$(STAGING_UNIT_TMPL)" > "$$tmp"; \
	$(STAGING_SCP) "$$tmp" "$(STAGING_HOST):$(STAGING_UNIT_PATH)"; \
	$(STAGING_SSH) $(STAGING_HOST) 'chmod 644 "$(STAGING_UNIT_PATH)" && systemctl daemon-reload'; \
	echo "unit установлен: $(STAGING_UNIT_PATH)"

deploy-staging: deploy-staging-binary deploy-staging-dll deploy-staging-unit
	@$(STAGING_SSH) $(STAGING_HOST) 'systemctl enable "$(STAGING_SERVICE)" && systemctl restart "$(STAGING_SERVICE)"'
	@$(MAKE) deploy-staging-smoke --no-print-directory

deploy-staging-smoke:
	@$(STAGING_SSH) $(STAGING_HOST) 'systemctl is-active --quiet "$(STAGING_SERVICE)"' || { \
		echo "FAIL: $(STAGING_SERVICE) не active"; \
		$(STAGING_SSH) $(STAGING_HOST) 'journalctl -u "$(STAGING_SERVICE)" -n 50 --no-pager'; exit 1; }
	@for i in $$(seq 1 15); do \
		$(STAGING_SSH) $(STAGING_HOST) 'ss -ltn | grep -q ":$(STAGING_PORT) "' && break; \
		if [ "$$i" = "15" ]; then \
			echo "FAIL: порт $(STAGING_PORT) не слушается"; \
			$(STAGING_SSH) $(STAGING_HOST) 'ss -ltn | grep ":$(STAGING_PORT) " || true; journalctl -u "$(STAGING_SERVICE)" -n 50 --no-pager'; \
			exit 1; \
		fi; \
		sleep 1; \
	done
	@$(STAGING_SSH) $(STAGING_HOST) 'journalctl -u "$(STAGING_SERVICE)" --no-pager | grep -q "server listening on"' || { \
		echo "FAIL: в journal нет строки server listening on"; \
		$(STAGING_SSH) $(STAGING_HOST) 'journalctl -u "$(STAGING_SERVICE)" -n 50 --no-pager'; exit 1; }
	@echo "OK   $(STAGING_SERVICE): active, порт $(STAGING_PORT), server listening"

deploy-staging-status:
	@$(STAGING_SSH) $(STAGING_HOST) 'systemctl status "$(STAGING_SERVICE)" --no-pager -l | head -25; echo; ss -ltnp | grep ":$(STAGING_PORT) " || true'

deploy-staging-logs:
	@$(STAGING_SSH) $(STAGING_HOST) 'journalctl -u "$(STAGING_SERVICE)" -n 120 --no-pager'

deploy-staging-restart:
	@$(STAGING_SSH) $(STAGING_HOST) 'systemctl restart "$(STAGING_SERVICE)"'
	@$(MAKE) deploy-staging-smoke --no-print-directory

rollback-staging:
	@$(STAGING_SSH) $(STAGING_HOST) 'test -f "$(STAGING_REMOTE_BIN).bak"' || { echo "FAIL: нет $(STAGING_REMOTE_BIN).bak"; exit 1; }
	@$(STAGING_SSH) $(STAGING_HOST) 'cp -f "$(STAGING_REMOTE_BIN).bak" "$(STAGING_REMOTE_BIN)" && chmod 755 "$(STAGING_REMOTE_BIN)" && systemctl restart "$(STAGING_SERVICE)"'
	@$(MAKE) deploy-staging-smoke --no-print-directory
