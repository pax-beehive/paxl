package adaptor

import "testing"

func TestConfigurationJSONCPreservesStringsAndAcceptsComments(t *testing.T) {
	document, err := parseConfigurationDocument(
		[]byte(
			"{\n// Server metadata\n\"url\":\"https://example.test/a//b\",\"quoted\":\"/* literal */\",\"values\":[1,2,],}\n",
		),
		"jsonc",
	)
	if err != nil {
		t.Fatal(err)
	}
	if document["url"] != "https://example.test/a//b" || document["quoted"] != "/* literal */" {
		t.Fatalf("String contents changed: %#v", document)
	}
}

func TestConfigurationTOMLReadsQuotedKeysAndInlineTables(t *testing.T) {
	document, err := parseConfigurationDocument(
		[]byte(
			"[mcp_servers.\"repo.tools\"]\ncommand = \"node\"\nenv = { API_TOKEN = \"private\", MODE = \"safe\" }\nargs = [\n\"server.js\",\n]\n",
		),
		"toml",
	)
	if err != nil {
		t.Fatal(err)
	}
	servers, _ := document["mcp_servers"].(map[string]any)
	server, _ := servers["repo.tools"].(map[string]any)
	if server["command"] != "node" {
		t.Fatalf("Quoted TOML key was not preserved: %#v", document)
	}
}

func TestConfigurationYAMLReadsInertTagsAndRejectsMultipleDocuments(t *testing.T) {
	document, err := parseConfigurationDocument(
		[]byte("name: example\nmetadata:\n  value: !!str 42\n"),
		"yaml",
	)
	if err != nil || document["name"] != "example" {
		t.Fatalf("Cannot read YAML metadata: %#v, %v", document, err)
	}
	if _, err := parseConfigurationDocument(
		[]byte("name: first\n---\nname: second\n"),
		"yaml",
	); err == nil {
		t.Fatal("Multiple YAML documents must not silently discard declarations")
	}
}
