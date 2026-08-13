.PHONY: compile test vet server_build server queues_build queues client tgbot
compile: ## Compile the proto file.
	protoc proto/connect.proto --go_out=. --go-grpc_out=. --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative

test:
	go test ./...

vet:
	go vet ./...

server_build:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-s -w" -o bin/server.exe .

## Build and run the Win32 adapter under Wine.
server: server_build
	mkdir -p logs
	wine64 bin/server.exe

queues_build:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-s -w" -o bin/queues.exe ./examples/queues

queues: queues_build
	wine64 bin/queues.exe

client: ## Build and run client.
	go build -ldflags "-s -w" -o bin/client ./examples/grpc-client
	bin/client

tgbot: ## Build and run telegram bot app.
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
