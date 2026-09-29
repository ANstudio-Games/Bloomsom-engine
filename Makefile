.PHONY: all build init init-realtime init-turnbased start status db-status test fmt vet clean

all: build

build:
	go build -o bloomsom main.go

init: build
	./bloomsom init --preset lobby-chat --yes

init-realtime: build
	./bloomsom init --preset realtime-action --yes --force

init-turnbased: build
	./bloomsom init --preset turn-based --yes --force

start: build
	./bloomsom start

status: build
	./bloomsom status

db-status: build
	./bloomsom db status

test:
	go test -race ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

clean:
	rm -f bloomsom bloomsom.db* bloomsom.yaml
