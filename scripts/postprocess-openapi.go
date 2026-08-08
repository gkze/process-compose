//go:build ignore

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

type replacement struct {
	description string
	filename    string
	old         []byte
	new         []byte
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: postprocess-openapi OPENAPI_OUTPUT_DIRECTORY")
		os.Exit(2)
	}

	jsonSecurity := []byte(`    "security": [
        {
            "ApiTokenAuth": []
        }
    ],`)
	jsonConditionalSecurity := []byte(`    "security": [
        {
            "ApiTokenAuth": []
        },
        {}
    ],`)
	yamlSecurity := []byte(`security:
    - ApiTokenAuth: []
servers:`)
	yamlConditionalSecurity := []byte(`security:
    - ApiTokenAuth: []
    - {}
servers:`)
	docsGoLogName := []byte(`                        "minLength": 1,
                        "type": "string",
                        "description": "Comma-separated process names to stream",
                        "name": "name",
                        "in": "query",
                        "required": true`)
	docsGoConstrainedLogName := []byte(`                        "minLength": 1,
                        "pattern": "[^,]",
                        "type": "string",
                        "description": "Comma-separated process names to stream",
                        "name": "name",
                        "in": "query",
                        "required": true`)
	jsonLogName := []byte(`                        "description": "Comma-separated process names to stream",
                        "in": "query",
                        "name": "name",
                        "required": true,
                        "schema": {
                            "minLength": 1,
                            "type": "string"
                        }`)
	jsonConstrainedLogName := []byte(`                        "description": "Comma-separated process names to stream",
                        "in": "query",
                        "name": "name",
                        "required": true,
                        "schema": {
                            "minLength": 1,
                            "pattern": "[^,]",
                            "type": "string"
                        }`)
	yamlLogName := []byte(`                - description: Comma-separated process names to stream
                  in: query
                  name: name
                  required: true
                  schema:
                    minLength: 1
                    type: string`)
	yamlConstrainedLogName := []byte(`                - description: Comma-separated process names to stream
                  in: query
                  name: name
                  required: true
                  schema:
                    minLength: 1
                    pattern: '[^,]'
                    type: string`)

	replacements := []replacement{
		{description: "conditional token security", filename: "docs.go", old: jsonSecurity, new: jsonConditionalSecurity},
		{description: "conditional token security", filename: "swagger.json", old: jsonSecurity, new: jsonConditionalSecurity},
		{description: "conditional token security", filename: "swagger.yaml", old: yamlSecurity, new: yamlConditionalSecurity},
		{description: "LogsStream name constraint", filename: "docs.go", old: docsGoLogName, new: docsGoConstrainedLogName},
		{description: "LogsStream name constraint", filename: "swagger.json", old: jsonLogName, new: jsonConstrainedLogName},
		{description: "LogsStream name constraint", filename: "swagger.yaml", old: yamlLogName, new: yamlConstrainedLogName},
	}
	for _, change := range replacements {
		if err := replaceExactlyOnce(filepath.Join(os.Args[1], change.filename), change.description, change.old, change.new); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func replaceExactlyOnce(filename, description string, old, new []byte) error {
	contents, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("read %s: %w", filename, err)
	}
	if count := bytes.Count(contents, old); count != 1 {
		return fmt.Errorf("%s contains the generated %s block %d times, want exactly once", filename, description, count)
	}
	info, err := os.Stat(filename)
	if err != nil {
		return fmt.Errorf("stat %s: %w", filename, err)
	}
	contents = bytes.Replace(contents, old, new, 1)
	if err := os.WriteFile(filename, contents, info.Mode()); err != nil {
		return fmt.Errorf("write %s: %w", filename, err)
	}
	return nil
}
