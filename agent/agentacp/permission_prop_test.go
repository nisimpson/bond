package agentacp

import (
	"context"
	"math/rand"
	"testing"
	"testing/quick"
)

// TestProperty_PermissionPolicyDecisionMapping verifies that permission policies
// produce the correct decisions based on tool name patterns:
//   - YOLOPolicy always approves regardless of tool name
//   - TrustPolicy approves read and write tools, denies other tools
//   - ReadPolicy approves only read tools, denies write and other tools
//
// **Validates: Requirements 5.3, 5.4**
func TestProperty_PermissionPolicyDecisionMapping(t *testing.T) {
	// Feature: acp-proxy, Property 6: Permission Policy Decision Mapping

	readTools := []string{
		"read_file", "list_directory", "get_status", "search_code", "find_pattern",
		"view_log", "show_config", "describe_resource", "cat_file", "head_output",
		"tail_log", "ls_dir", "stat_file",
	}
	writeTools := []string{
		"write_file", "create_dir", "update_config", "delete_resource", "remove_file",
		"edit_document", "modify_setting", "put_object", "post_data", "set_value",
		"mkdir_path", "mv_file", "cp_data", "rename_item",
	}
	otherTools := []string{
		"execute_command", "deploy_app", "analyze", "run_tests",
		"format_code", "compile", "build", "benchmark",
	}

	f := func(seed int64) bool {
		rnd := rand.New(rand.NewSource(seed))

		// Pick random tools from each category.
		readTool := readTools[rnd.Intn(len(readTools))]
		writeTool := writeTools[rnd.Intn(len(writeTools))]
		otherTool := otherTools[rnd.Intn(len(otherTools))]

		ctx := context.Background()
		yolo := YOLOPolicy()
		trust := TrustPolicy()
		read := ReadPolicy()

		// YOLO always approves.
		for _, tool := range []string{readTool, writeTool, otherTool} {
			req := PermissionRequest{ToolName: tool}
			if yolo(ctx, req) != Approve {
				t.Logf("YOLO should approve %q", tool)
				return false
			}
		}

		// Trust approves read and write, denies other.
		if trust(ctx, PermissionRequest{ToolName: readTool}) != Approve {
			t.Logf("Trust should approve read tool %q", readTool)
			return false
		}
		if trust(ctx, PermissionRequest{ToolName: writeTool}) != Approve {
			t.Logf("Trust should approve write tool %q", writeTool)
			return false
		}
		if trust(ctx, PermissionRequest{ToolName: otherTool}) != Deny {
			t.Logf("Trust should deny other tool %q", otherTool)
			return false
		}

		// Read approves only read, denies write and other.
		if read(ctx, PermissionRequest{ToolName: readTool}) != Approve {
			t.Logf("Read should approve read tool %q", readTool)
			return false
		}
		if read(ctx, PermissionRequest{ToolName: writeTool}) != Deny {
			t.Logf("Read should deny write tool %q", writeTool)
			return false
		}
		if read(ctx, PermissionRequest{ToolName: otherTool}) != Deny {
			t.Logf("Read should deny other tool %q", otherTool)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Permission policy decision mapping property failed: %v", err)
	}
}
