package conf_test

import (
	"encoding/json"
	"testing"

	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/transport/internet/reality"
)

// Spofy fork guards for REALITY server settings (see third_party/reality/SPOFY.md).
func TestSpofyREALITYServerDefaults(t *testing.T) {
	build := func(extra string) *reality.Config {
		t.Helper()
		raw := `{"target":"127.0.0.1:443","serverNames":["example.com"],` +
			`"privateKey":"aGSYystUbf59_9_6LKRxD27rmSW_-2_nyd9YG_Gwbks","shortIds":[""]` + extra + `}`
		c := new(conf.REALITYConfig)
		if err := json.Unmarshal([]byte(raw), c); err != nil {
			t.Fatal(err)
		}
		m, err := c.Build()
		if err != nil {
			t.Fatal(err)
		}
		return m.(*reality.Config)
	}

	def := build("")
	if def.RequireMlkem {
		t.Error("requireMlkem must default to false so legacy fingerprints (edge, ios, qq) keep working")
	}
	// Upstream once forced minClientVer 26.3.27 when unset (now commented out). An implicit minimum would
	// lock out every user app with an older core, so fail loudly if a merge brings it back.
	if def.MinClientVer != nil || def.MaxClientVer != nil {
		t.Errorf("implicit client version bounds: min=%v max=%v", def.MinClientVer, def.MaxClientVer)
	}
	if !build(`,"requireMlkem":true`).RequireMlkem {
		t.Error(`"requireMlkem": true was not applied`)
	}
	if !build(`,"requireMlkem":true`).GetREALITYConfig().RequireMlkem {
		t.Error("RequireMlkem not passed to the REALITY server config")
	}
}
