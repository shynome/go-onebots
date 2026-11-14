VERSION := $(shell git describe --tags --always --dirty)

.PHONY: build docker push

build:
	CGO_ENABLED=0 go build -ldflags="-X 'main.Version=${VERSION}' -s -w" -o go-onebots .

docker: build
	docker build . -t shynome/go-onebots:${VERSION}

push: docker
	docker push shynome/go-onebots:${VERSION}
