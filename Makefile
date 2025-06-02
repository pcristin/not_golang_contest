.PHONY: build run up up-build down logs clean
APP_NAME := flash_sale

build:
	go build -v -o bin/$(APP_NAME) cmd/server/main.go

run:
	go run cmd/server/main.go

up:
	docker-compose up -d

up-build:
	docker-compose up -d --build

down:
	docker-compose down

logs:
	docker-compose logs -f app

clean:
	docker-compose down -v
	docker system prune -f
	rm -rf bin/

megaload:
	docker-compose up -d
	go run cmd/megaload/main.go
	