package engine

import "encoding/json"

type TargetInfo struct {
	Attached         bool   `json:"attached"`
	BrowserContextID string `json:"browserContextId"`
	CanAccessOpener  bool   `json:"canAccessOpener"`
	ParentID         string `json:"parentId"`
	OpenerFrameID    string `json:"openerFrameId"`
	OpenerID         string `json:"openerId"`
	ParentFrameID    string `json:"parentFrameId"`
	TargetID         string `json:"targetId"`
	Title            string `json:"title"`
	Type             string `json:"type"`
	URL              string `json:"url"`
}

func MustTargetInfo(response *CDPResponse) TargetInfo {
	var targetInfo TargetInfo
	if info, ok := response.Params["targetInfo"].(map[string]any); ok {
		marshal, _ := json.Marshal(info)
		_ = json.Unmarshal(marshal, &targetInfo)
	}
	return targetInfo
}

type TargetSession struct {
	TargetID  string
	SessionID string
	Type      string
	URL       string
	ParentID  string
	OpenerID  string
	FrameID   string
	PageID    string
}

type BindingCalled struct {
	Args []struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"args"`
	Context struct {
		AuxData struct {
			FrameID   string `json:"frameId"`
			IsDefault bool   `json:"isDefault"`
			Type      string `json:"type"`
		} `json:"auxData"`
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Origin   string `json:"origin"`
		UniqueID string `json:"uniqueId"`
	} `json:"context"`
	ExecutionContextID int `json:"executionContextId"`
	Frame              struct {
		AdFrameStatus struct {
			AdFrameType string `json:"adFrameType"`
		} `json:"adFrameStatus"`
		CrossOriginIsolatedContextType string        `json:"crossOriginIsolatedContextType"`
		DomainAndRegistry              string        `json:"domainAndRegistry"`
		GatedAPIFeatures               []interface{} `json:"gatedAPIFeatures"`
		ID                             string        `json:"id"`
		LoaderID                       string        `json:"loaderId"`
		MimeType                       string        `json:"mimeType"`
		SecureContextType              string        `json:"secureContextType"`
		SecurityOrigin                 string        `json:"securityOrigin"`
		SecurityOriginDetails          struct {
			IsLocalhost bool `json:"isLocalhost"`
		} `json:"securityOriginDetails"`
		URL string `json:"url"`
	} `json:"frame"`
	FrameID        string `json:"frameId"`
	LoaderID       string `json:"loaderId"`
	Name           string `json:"name"`
	NavigationType string `json:"navigationType"`
	Payload        string `json:"payload"`
	StackTrace     struct {
		CallFrames []struct {
			ColumnNumber int    `json:"columnNumber"`
			FunctionName string `json:"functionName"`
			LineNumber   int    `json:"lineNumber"`
			ScriptID     string `json:"scriptId"`
			URL          string `json:"url"`
		} `json:"callFrames"`
	} `json:"stackTrace"`
	Timestamp      float64  `json:"timestamp"`
	Type           string   `json:"type"`
	URL            string   `json:"url"`
	UserGesture    bool     `json:"userGesture"`
	WindowFeatures []string `json:"windowFeatures"`
	WindowName     string   `json:"windowName"`
}
