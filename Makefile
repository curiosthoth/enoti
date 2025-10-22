.PHONY: build build-lambda test clean run help

# Binary name
BINARY_NAME=enoti
LAMBDA_BINARY_NAME=bootstrap

# Build directory
BUILD_DIR=bin

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOCLEAN=$(GOCMD) clean
GOMOD=$(GOCMD) mod

# Main package paths
MAIN_PATH=./cmd/enoti
LAMBDA_PATH=./cmd/lambda-sqs

# Build the binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -tags=!lambda -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

# Build the Lambda binary
build-lambda:
	@echo "Building Lambda $(LAMBDA_BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GOBUILD) -tags=lambda -o $(BUILD_DIR)/$(LAMBDA_BINARY_NAME) $(LAMBDA_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(LAMBDA_BINARY_NAME)"

# Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

# Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	$(GOTEST) -v -cover ./...

# Clean build artifacts
clean:
	@echo "Cleaning..."
	$(GOCLEAN)
	@rm -rf $(BUILD_DIR)
	@echo "Clean complete"

# Run the application
run: build
	@echo "Running $(BINARY_NAME)..."
	./$(BUILD_DIR)/$(BINARY_NAME)

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy

# Show help
help:
	@echo "Available targets:"
	@echo "  build          - Build the HTTP server binary"
	@echo "  build-lambda   - Build the Lambda binary (bootstrap)"
	@echo "  test           - Run tests"
	@echo "  test-coverage  - Run tests with coverage"
	@echo "  clean          - Remove build artifacts"
	@echo "  run            - Build and run the application"
	@echo "  deps           - Download and tidy dependencies"
	@echo "  help           - Show this help message"
