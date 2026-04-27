HOST ?= user@your-server

.PHONY: client server deploy

client:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o dist/visionary ./cmd/visionary

server:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o dist/visionary-server ./cmd/visionary-server

deploy: server
	scp dist/visionary-server $(HOST):/usr/local/bin/visionary-server
	ssh $(HOST) systemctl restart visionary
