package authority

import "testing"

func TestValidAcceptsExactlyTheFourLevels(t *testing.T) {
	for _, ok := range []string{"L1", "L2", "L3", "L4"} {
		if !Valid(ok) {
			t.Errorf("%q must be valid", ok)
		}
	}
	for _, bad := range []string{"", "L0", "L5", "l1", "L1 ", "read", "L"} {
		if Valid(bad) {
			t.Errorf("%q must be invalid (G1: L1..L4 only, no L5)", bad)
		}
	}
}

func TestVocabularyIsThePinnedContract(t *testing.T) {
	for got, want := range map[string]string{
		MetaKey: "com.warehouse/authority", AttrOperationName: "gen_ai.operation.name", AttrAgentName: "gen_ai.agent.name",
		AttrAuthority: "warehouse.authority", OpExecuteTool: "execute_tool", OpInvokeAgent: "invoke_agent", AgentName: "warehouse-ops-agent",
	} {
		if got != want {
			t.Errorf("constant = %q, contracts.md pins %q", got, want)
		}
	}
}
