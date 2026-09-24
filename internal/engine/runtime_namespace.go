package engine

import (
	"strings"
)

type NamespaceRegistration struct {
	Namespace      RuntimeNamespace
	WorldName      string
	MainWorld      bool
	DefaultEnabled bool
}

func defaultNamespaceRegistrations() map[RuntimeNamespace]NamespaceRegistration {
	regs := map[RuntimeNamespace]NamespaceRegistration{}
	for _, reg := range []NamespaceRegistration{
		{Namespace: NamespacePageMain, MainWorld: true, DefaultEnabled: true},
		{Namespace: NamespaceMainRuntime, MainWorld: true, DefaultEnabled: false},
		{Namespace: NamespaceIsolatedCore, WorldName: worldNameIsolatedCore, DefaultEnabled: true},
		{Namespace: NamespaceOverlay, DefaultEnabled: true},
	} {
		regs[reg.Namespace] = reg
	}
	return regs
}

func normalizeRuntimeNamespace(ns RuntimeNamespace) RuntimeNamespace {
	if ns == "" {
		return NamespaceIsolatedCore
	}
	return ns
}

func (r *BrowserManager) namespaceRegistration(ns RuntimeNamespace) NamespaceRegistration {
	ns = normalizeRuntimeNamespace(ns)
	if r == nil {
		return defaultNamespaceRegistrations()[ns]
	}
	r.scriptMu.RLock()
	defer r.scriptMu.RUnlock()
	if r.namespaces != nil {
		if reg, ok := r.namespaces[ns]; ok {
			return reg
		}
	}
	return defaultNamespaceRegistrations()[ns]
}

func (r *BrowserManager) namespaceWorldName(ns RuntimeNamespace) string {
	return r.namespaceRegistration(ns).WorldName
}

func namespaceMatchesContext(reg NamespaceRegistration, info executionContextInfo) bool {
	if reg.Namespace == NamespaceOverlay {
		return false
	}
	if reg.MainWorld || reg.Namespace == NamespacePageMain || reg.Namespace == NamespaceMainRuntime {
		return info.IsDefault
	}
	return strings.TrimSpace(info.Name) == strings.TrimSpace(reg.WorldName)
}
