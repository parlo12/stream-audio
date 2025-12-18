#!/bin/bash

# Book Search API Test Script
# Tests the new POST /user/search-books endpoint

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
BASE_URL="http://68.183.22.205:8083"
AUTH_URL="http://68.183.22.205:8080"

echo -e "${BLUE}==================================${NC}"
echo -e "${BLUE}Book Search API Test Script${NC}"
echo -e "${BLUE}==================================${NC}\n"

# Function to print test results
print_test() {
    echo -e "${YELLOW}TEST: $1${NC}"
}

print_success() {
    echo -e "${GREEN}✓ PASSED: $1${NC}\n"
}

print_error() {
    echo -e "${RED}✗ FAILED: $1${NC}\n"
}

# Step 1: Login to get token
print_test "Step 1: Login to get JWT token"

read -p "Enter username: " USERNAME
read -sp "Enter password: " PASSWORD
echo ""

LOGIN_RESPONSE=$(curl -s -X POST "$AUTH_URL/login" \
  -H "Content-Type: application/json" \
  -d "{\"username\": \"$USERNAME\", \"password\": \"$PASSWORD\"}")

TOKEN=$(echo $LOGIN_RESPONSE | grep -o '"token":"[^"]*' | cut -d'"' -f4)

if [ -z "$TOKEN" ]; then
    print_error "Failed to get token. Response: $LOGIN_RESPONSE"
    exit 1
else
    print_success "Got JWT token: ${TOKEN:0:20}..."
fi

# Step 2: Test valid search
print_test "Step 2: Search for 'Harry Potter'"

SEARCH_RESPONSE=$(curl -s -X POST "$BASE_URL/user/search-books" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"query": "Harry Potter"}')

echo "Response:"
echo "$SEARCH_RESPONSE" | python3 -m json.tool 2>/dev/null || echo "$SEARCH_RESPONSE"

if echo "$SEARCH_RESPONSE" | grep -q '"results"'; then
    print_success "Search returned results"
else
    print_error "Search did not return expected results"
fi

echo ""

# Step 3: Test another search
print_test "Step 3: Search for 'The Lord of the Rings'"

SEARCH_RESPONSE_2=$(curl -s -X POST "$BASE_URL/user/search-books" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"query": "The Lord of the Rings"}')

echo "Response:"
echo "$SEARCH_RESPONSE_2" | python3 -m json.tool 2>/dev/null || echo "$SEARCH_RESPONSE_2"

if echo "$SEARCH_RESPONSE_2" | grep -q '"results"'; then
    print_success "Search returned results"
else
    print_error "Search did not return expected results"
fi

echo ""

# Step 4: Test missing query parameter
print_test "Step 4: Test missing query parameter (should fail with 400)"

ERROR_RESPONSE=$(curl -s -X POST "$BASE_URL/user/search-books" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}')

echo "Response: $ERROR_RESPONSE"

if echo "$ERROR_RESPONSE" | grep -q "Query parameter is required"; then
    print_success "Correctly rejected missing query"
else
    print_error "Should have rejected missing query"
fi

echo ""

# Step 5: Test empty query
print_test "Step 5: Test empty query (should fail with 400)"

ERROR_RESPONSE_2=$(curl -s -X POST "$BASE_URL/user/search-books" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"query": ""}')

echo "Response: $ERROR_RESPONSE_2"

if echo "$ERROR_RESPONSE_2" | grep -q "Query cannot be empty"; then
    print_success "Correctly rejected empty query"
else
    print_error "Should have rejected empty query"
fi

echo ""

# Step 6: Test missing token
print_test "Step 6: Test missing token (should fail with 401)"

ERROR_RESPONSE_3=$(curl -s -X POST "$BASE_URL/user/search-books" \
  -H "Content-Type: application/json" \
  -d '{"query": "test"}')

echo "Response: $ERROR_RESPONSE_3"

if echo "$ERROR_RESPONSE_3" | grep -q -i "token\|unauthorized"; then
    print_success "Correctly rejected missing token"
else
    print_error "Should have rejected missing token"
fi

echo ""

# Summary
echo -e "${BLUE}==================================${NC}"
echo -e "${BLUE}Test Summary${NC}"
echo -e "${BLUE}==================================${NC}"
echo -e "${GREEN}✓ All basic tests completed${NC}"
echo -e "${YELLOW}Note: Review responses above for detailed results${NC}\n"

# Optional: Interactive search
echo -e "${YELLOW}Would you like to run an interactive search? (y/n)${NC}"
read -r INTERACTIVE

if [ "$INTERACTIVE" = "y" ]; then
    echo ""
    read -p "Enter search query: " QUERY

    echo -e "\n${BLUE}Searching for: $QUERY${NC}\n"

    INTERACTIVE_RESPONSE=$(curl -s -X POST "$BASE_URL/user/search-books" \
      -H "Authorization: Bearer $TOKEN" \
      -H "Content-Type: application/json" \
      -d "{\"query\": \"$QUERY\"}")

    echo "$INTERACTIVE_RESPONSE" | python3 -m json.tool 2>/dev/null || echo "$INTERACTIVE_RESPONSE"
fi

echo -e "\n${GREEN}Test script completed!${NC}"
