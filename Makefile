.PHONY: compile
compile: ## Compile the proto file.
	protoc -I proto proto/connect.proto --go_out=plugins=grpc:proto/

.PHONY: server

server_build:
	CGO_ENABLED=1 CC="x86_64-w64-mingw32-gcc" CXX="x86_64-w64-mingw32-g++" GOOS=windows GOARCH=amd64 go build -race -ldflags "-extldflags -static -s -w" -o bin/server.exe main.go

## Build and run server. brew install mingw-w64
server: server_build
	mkdir -p logs
	wine64 bin/server.exe

queues_build:
	CGO_ENABLED=1 CC="x86_64-w64-mingw32-gcc" CXX="x86_64-w64-mingw32-g++" GOOS=windows GOARCH=amd64 go build -race -ldflags "-extldflags -static -s -w" -o bin/queues.exe examples/queues/main.go

queues: queues_build
	wine64 bin/queues.exe

.PHONY: client
client: ## Build and run client.
	go build -race -ldflags "-s -w" -o bin/client client/main.go
	bin/client

.PHONY: tgbot
tgbot: ## Build and run telegram bot app.
	go build -race -ldflags "-s -w" -o bin/tgbot examples/telegram-bot/main.go
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