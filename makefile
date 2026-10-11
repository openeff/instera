.PHONY: dev build run

APP_NAME := instera
WATCHER := ./tools/devwatch

ifeq ($(OS),Windows_NT)
	EXE := .exe
else
	EXE :=
endif

build:
	go build -o ./bin/$(APP_NAME)$(EXE) ./cmd

run: build
	./bin/$(APP_NAME)$(EXE)

dev:
	go run $(WATCHER)