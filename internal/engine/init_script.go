package engine

import (
	"fmt"
	"strings"
)

type InitScript struct {
	OnDemand  bool
	Name      string
	Namespace RuntimeNamespace
	Exec      string
	Probe     string
	Cleanup   string
	Label     string
}

func (s InitScript) execSourceWithAction(action string) string {
	return withSourceURL(s.Exec, s.sourceURL(action, "init"))
}

func (s InitScript) probeSourceWithAction(action string) string {
	return withSourceURL(s.Probe, s.sourceURL(action, "probe"))
}

func (s InitScript) cleanupSource() string {
	return withSourceURL(s.Cleanup, s.sourceURL("", "cleanup"))
}

func (r *BrowserManager) initScriptSource(script InitScript, action, phase string) string {
	var source string
	switch phase {
	case "exec":
		source = script.Exec
	case "probe", "verify":
		source = script.Probe
	case "cleanup":
		source = script.Cleanup
	default:
		panic("invalid init script phase: " + phase)
	}
	if strings.TrimSpace(source) == "" {
		return source
	}
	if r.namespaceRegistration(script.Namespace).MainWorld {
		return source
	}
	if phase == "verify" {
		return withSourceURL(source, script.sourceURL(action, phase))
	}
	registration := r.namespaceRegistration(script.Namespace)
	owner := strings.TrimSpace(r.runtimeFields.ScriptLabel)
	if registration.MainWorld || registration.Namespace == NamespaceOverlay || owner == "" {
		switch phase {
		case "exec":
			return script.execSourceWithAction(action)
		case "probe":
			return script.probeSourceWithAction(action)
		default:
			return script.cleanupSource()
		}
	}

	if phase == "exec" {
		r.bindingMu.RLock()
		var names []string
		for name, ns := range r.bindingNamespaces {
			if r.namespaceWorldName(ns) == registration.WorldName {
				names = append(names, jsStringLiteral(name))
			}
		}
		r.bindingMu.RUnlock()
		if len(names) > 0 {
			source = `(async()=>{const names=[` + strings.Join(names, ",") + `];const deadline=Date.now()+10000;while(names.some(name=>typeof globalThis[name]!=="function")){if(Date.now()>deadline)throw new Error("binding initialization timed out");await new Promise(resolve=>setTimeout(resolve,10));}return await ` + source + `})()`
		}
	}
	nameLiteral := jsStringLiteral(script.Name)
	ownerLiteral := jsStringLiteral(owner)
	registry := `let lifecycle = window.__cdp_runtime_lifecycle;
	if (!lifecycle) {
		lifecycle = {};
		Object.defineProperty(window, '__cdp_runtime_lifecycle', {
			value: lifecycle, configurable: true, writable: true, enumerable: false,
		});
	}
	let owners = lifecycle.owners;
	if (!owners) {
		owners = Object.create(null);
		Object.defineProperty(lifecycle, 'owners', {
			value: owners, configurable: true, writable: true, enumerable: false,
		});
	}`
	var wrapped string
	switch phase {
	case "exec":
		wrapped = fmt.Sprintf(`(async () => {
		%s
		owners[%s] = %s;
		await %s
	})()`, registry, nameLiteral, ownerLiteral, source)
	case "probe":
		wrapped = fmt.Sprintf(`(async () => {
		%s
		const ready = await %s
		if (ready === true) owners[%s] = %s;
		return ready;
	})()`, registry, source, nameLiteral, ownerLiteral)
	case "cleanup":
		wrapped = fmt.Sprintf(`(async () => {
		%s
		if (owners[%s] !== %s) return false;
		try {
			await %s
			return true;
		} finally {
			if (owners[%s] === %s) delete owners[%s];
		}
	})()`, registry, nameLiteral, ownerLiteral, source, nameLiteral, ownerLiteral, nameLiteral)
	}
	sourcePhase := phase
	if phase == "exec" {
		sourcePhase = "init"
	}
	return withSourceURL(wrapped, script.sourceURL(action, sourcePhase))
}

func (s InitScript) sourceURL(action string, phase string) string {
	_ = action
	label := sanitizeInitScriptToken(s.Label)
	if label == "" {
		label = "script"
	}
	parts := []string{label, phase}
	return strings.Join(parts, "_") + ".js"
}

func sanitizeInitScriptToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(value))
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastUnderscore = false
		case r >= 'A' && r <= 'Z':
			b.WriteByte(byte(r + ('a' - 'A')))
			lastUnderscore = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

func withSourceURL(script string, sourceURL string) string {
	if strings.TrimSpace(script) == "" {
		return script
	}
	if strings.Contains(script, "\n//# sourceURL=") || strings.HasSuffix(strings.TrimSpace(script), "//# sourceURL="+sourceURL) {
		return script
	}
	if strings.HasSuffix(script, "\n") {
		return script + "//# sourceURL=" + sourceURL
	}
	return script + "\n//# sourceURL=" + sourceURL
}
