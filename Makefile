BIN_DIR := bin
BIN_NAME := nlb

.PHONY: run
run: 
	@go run . ./examples/example_config.json
.PHONY: test
test:
	go test -v -timeout 30s ./...
.PHONY: build
build:
	go build -o $(BIN_DIR)/$(BIN_NAME) ./...
.PHONY: clean
clean:
	rm -rf $(BIN_DIR)
