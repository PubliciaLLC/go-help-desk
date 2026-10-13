package config_test

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// TestComposeForwardedEnvVars verifies that every documented environment variable
// is forwarded in docker-compose.yml's app service, or is listed in Compose-only
// variables (GHD_VERSION, COMPOSE_PROFILES).
func TestComposeForwardedEnvVars(t *testing.T) {
	// Read .env.example
	envFile := "../../../docker/.env.example"
	envContent, err := os.ReadFile(envFile)
	require.NoError(t, err, "failed to read %s", envFile)

	// Parse variable names from .env.example
	// Variables can be commented or uncommented, but named anywhere as VAR_NAME=value or # VAR_NAME=value
	vars := parseEnvVars(string(envContent))

	// Read docker-compose.yml
	composeFile := "../../../docker/docker-compose.yml"
	composeContent, err := os.ReadFile(composeFile)
	require.NoError(t, err, "failed to read %s", composeFile)

	// Parse YAML to extract app service environment variables
	var compose map[string]interface{}
	err = yaml.Unmarshal(composeContent, &compose)
	require.NoError(t, err, "failed to parse docker-compose.yml")

	services, ok := compose["services"].(map[string]interface{})
	require.True(t, ok, "services section not found in docker-compose.yml")

	appService, ok := services["app"].(map[string]interface{})
	require.True(t, ok, "app service not found in docker-compose.yml")

	environment, ok := appService["environment"].(map[string]interface{})
	require.True(t, ok, "environment section not found in app service")

	// Build a set of environment variables in Compose (keys)
	composeVars := make(map[string]bool)
	for k := range environment {
		composeVars[k] = true
	}

	// Compose-only variables that don't need to be in .env.example
	composeOnly := map[string]bool{
		"GHD_VERSION":      true,
		"COMPOSE_PROFILES": true,
		"DATABASE_URL":     true, // Fixed in compose
		"HTTP_PORT":        true, // Fixed in compose
		"ATTACHMENT_DIR":   true, // Fixed in compose
	}

	// Check each documented variable
	for _, varName := range vars {
		if composeOnly[varName] {
			continue
		}
		require.True(t, composeVars[varName],
			"variable %s from .env.example is not forwarded in docker-compose.yml app.environment", varName)
	}
}

// parseEnvVars extracts all variable names from .env.example
func parseEnvVars(content string) []string {
	var vars []string
	scanner := bufio.NewScanner(strings.NewReader(content))
	// Match lines like: VAR_NAME=value or # VAR_NAME=value
	re := regexp.MustCompile(`^#?\s*([A-Z_][A-Z0-9_]*)\s*=`)

	seen := make(map[string]bool)
	for scanner.Scan() {
		line := scanner.Text()
		matches := re.FindStringSubmatch(line)
		if len(matches) > 1 {
			varName := matches[1]
			if !seen[varName] {
				vars = append(vars, varName)
				seen[varName] = true
			}
		}
	}
	return vars
}

// TestComposeEnvDefaults verifies that forwarded variables have sensible defaults
func TestComposeEnvDefaults(t *testing.T) {
	composeFile := "../../../docker/docker-compose.yml"
	composeContent, err := os.ReadFile(composeFile)
	require.NoError(t, err, "failed to read %s", composeFile)

	var compose map[string]interface{}
	err = yaml.Unmarshal(composeContent, &compose)
	require.NoError(t, err, "failed to parse docker-compose.yml")

	services := compose["services"].(map[string]interface{})
	appService := services["app"].(map[string]interface{})
	environment := appService["environment"].(map[string]interface{})

	// Check for expected variables with correct defaults
	cases := []struct {
		varName       string
		expectedValue string // substring to find in the value
	}{
		{"AUTH_RATE_LIMIT_PER_MINUTE", "10"},
		{"AUTH_THROTTLE_DELAY", "1s"},
		{"LOG_LEVEL", "info"},
	}

	for _, tc := range cases {
		value, ok := environment[tc.varName]
		require.True(t, ok, "variable %s not found in compose environment", tc.varName)
		require.Contains(t, value.(string), tc.expectedValue,
			"default value for %s does not contain expected %s; got %s",
			tc.varName, tc.expectedValue, value.(string))
	}
}
