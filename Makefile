.PHONY: compile
compile: ## Compile the proto file.
	protoc -I proto proto/connect.proto --go_out=plugins=grpc:proto/

.PHONY: server
server: ## Build and run server. brew install mingw-w64
	CGO_ENABLED=1 CC="x86_64-w64-mingw32-gcc" GOOS=windows GOARCH=amd64 go build -race -ldflags "-s -w" -o bin/server.exe server/main.go
	wine64 bin/server.exe

.PHONY: client
client: ## Build and run client.
	go build -race -ldflags "-s -w" -o bin/client client/main.go
	bin/client

.PHONY: client
tgbot: ## Build and run telegram bot app.
	go build -race -ldflags "-s -w" -o bin/tgbot examples/telegram-bot/main.go
	bin/tgbot
