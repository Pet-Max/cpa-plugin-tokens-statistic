package plugin

import (
	"encoding/json"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/apikey"
	"os/exec"
	"strings"
	"testing"
)

func TestAPIKeyDashboardContracts(t *testing.T) {
	for _, required := range []string{
		`id="apiKeyControls"`,
		`id="apiKeySecurityWarning"`,
		`id="apiKeyFilterButton"`,
		`id="apiKeyFilterMenu"`,
		`id="apiKeyLabelDialog"`,
		"selectedAPIKeyRefs=[]",
		"function apiKeyRefsQuery(params)",
		"params.append('api_key_ref',ref)",
		"checkbox.dataset.apiKeyRef=item.ref",
		"requestAPIKeyFilterAll.dataset.apiKeyFilterAll='true'",
		"requestAPIKeyFilterAll.indeterminate=selectedAPIKeyRefs.length>0",
		"selectedAPIKeyRefs=selectedAPIKeyRefs.filter(function(ref){return known.has(ref);});",
		"function restoreAPIKeyFilterFocus(menu,ref)",
		"function currentAPIKeyFilterFocusRef()",
		"function keepAPIKeyFilterMenuOpen(event){event.stopPropagation();}",
		"requestAPIKeyFilterAll.addEventListener('click',keepAPIKeyFilterMenuOpen)",
		"checkbox.addEventListener('click',keepAPIKeyFilterMenuOpen)",
		"var focusRef=currentAPIKeyFilterFocusRef()",
		"renderAPIKeyFilter(focusRef)",
		"setSelectedAPIKeyRefs([], 'all')",
		"setSelectedAPIKeyRefs(next,item.ref)",
		"activeDropdown===document.getElementById('apiKeyFilterMenu')",
		"positionAPIKeyFilterMenu();",
		"generation_unavailable:'apiKey.generationUnavailable'",
		"ciphertext_missing:'apiKey.ciphertextMissing'",
		"ciphertext_invalid:'apiKey.ciphertextInvalid'",
		"identity_missing:'apiKey.identityMissing'",
		"apiKeyOptions=next;var known=new Set(next.map(function(item){return item.ref;}))",
		"initializeAPIKeyFullMode(payload)",
		"initializeAPIKeyFullMode(payload)",
		"statsInitialURL",
		"statsTrendsURL",
		"statsGroupsURL",
		".apikey-security-warning[hidden]{display:none!important}",
		"api_key_uses_default_secret",
		"api(managementBase+'/api-key-info')",
		"api(managementBase+'/api-key-labels'",
		"body:JSON.stringify({ref:editingAPIKeyRef,label:value})",
		"'Authorization':'Bearer '+managementKey",
	} {
		if !strings.Contains(dashboardHTML, required) {
			t.Fatalf("full dashboard missing API-key contract %q", required)
		}
	}
	if strings.Contains(dashboardHTML, "/*FULL_MODE_APIKEY_") {
		t.Fatal("generated dashboard contains unresolved API-key placeholder")
	}
	if strings.Contains(dashboardHTML, "X-Full-Mode-Session") || strings.Contains(dashboardHTML, "/full-mode/") {
		t.Fatal("generated dashboard still references the removed full-mode session flow")
	}
}

func TestAPIKeyFilterIsRenderedBesideSourceFilter(t *testing.T) {
	if strings.Contains(dashboardHTML, "/*FULL_MODE_APIKEY_FILTER*/") {
		t.Fatal("full dashboard contains unresolved API-key filter placeholder")
	}
	rangeButtonIndex := strings.Index(dashboardHTML, `id="rangeButton"`)
	apiKeyIndex := strings.Index(dashboardHTML, `id="apiKeyFilter"`)
	rangeButtonEnd := rangeButtonIndex + len(`id="rangeButton"`)
	if rangeButtonIndex < 0 || apiKeyIndex < 0 || apiKeyIndex < rangeButtonEnd {
		t.Fatal("API-key filter is not rendered after the range control in the top filters")
	}
	if strings.Contains(dashboardHTML, `class="apikey-controls"`) && strings.Contains(dashboardHTML, `id="apiKeyFilter"`) {
		controlsIndex := strings.Index(dashboardHTML, `class="apikey-controls"`)
		if controlsIndex < apiKeyIndex {
			t.Fatal("API-key filter remains inside the standalone controls panel")
		}
	}
	for _, required := range []string{
		`rangeButton.insertAdjacentElement('afterend',select)`,
		`select.id='sourceFilter'`,
	} {
		if !strings.Contains(dashboardHTML, required) {
			t.Fatalf("full dashboard missing source-filter placement contract %q", required)
		}
	}
}

func TestAPIKeyLocaleCatalog(t *testing.T) {
	required := []string{
		"table.apiKey", "apiKey.filter", "apiKey.all", "apiKey.editLabel",
		"apiKey.label", "apiKey.labelPlaceholder", "apiKey.labelHint",
		"apiKey.saveLabel", "apiKey.deleteLabel", "apiKey.trackingDisabled",
		"apiKey.plaintextUnavailable", "apiKey.generationUnavailable",
		"apiKey.ciphertextMissing", "apiKey.ciphertextInvalid", "apiKey.identityMissing", "apiKey.sourceMissing",
		"apiKey.defaultSecretWarning",
		"apiKey.labelTooLong", "apiKey.saveFailed", "apiKey.filterMenu", "apiKey.filterSelected", "apiKey.filterHint",
	}
	for _, code := range []string{"en", "zh-CN", "zh-TW", "ru"} {
		data, err := localeFS.ReadFile("web/locales/" + code + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var catalog map[string]string
		if err := json.Unmarshal(data, &catalog); err != nil {
			t.Fatalf("locale %s: %v", code, err)
		}
		for _, key := range required {
			if catalog[key] == "" {
				t.Fatalf("locale %s missing %q", code, key)
			}
		}
		if !strings.Contains(catalog["apiKey.defaultSecretWarning"], apikey.DefaultSecret) || !strings.Contains(catalog["apiKey.defaultSecretWarning"], "32") {
			t.Fatalf("locale %s warning does not explain the default and minimum strength", code)
		}
	}
}

func TestGeneratedDashboardJavaScriptSyntax(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not available")
	}
	for name, html := range map[string]string{"dashboard": dashboardHTML} {
		t.Run(name, func(t *testing.T) {
			remaining := html
			for index := 0; ; index++ {
				start := strings.Index(remaining, "<script")
				if start < 0 {
					break
				}
				openEnd := strings.Index(remaining[start:], ">")
				if openEnd < 0 {
					t.Fatal("unterminated script tag")
				}
				openEnd += start
				closeAt := strings.Index(remaining[openEnd+1:], "</script>")
				if closeAt < 0 {
					t.Fatal("unterminated script body")
				}
				closeAt += openEnd + 1
				script := remaining[openEnd+1 : closeAt]
				command := exec.Command(node, "--check", "-")
				command.Stdin = strings.NewReader(script)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("script %d syntax: %v\n%s", index, err, output)
				}
				remaining = remaining[closeAt+len("</script>"):]
			}
		})
	}
}
