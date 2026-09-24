// Package bindings contains CDP event payloads shared by binding implementations.
package binding

// BindingCalledEvent describes a Runtime.bindingCalled event.
type BindingCalledEvent struct {
	Name               string `json:"name"`
	Payload            string `json:"payload"`
	ExecutionContextID int    `json:"executionContextId"`
}
