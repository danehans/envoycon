package main

import (
  "github.com/tetratelabs/proxy-wasm-go-sdk/proxywasm"
  "github.com/tetratelabs/proxy-wasm-go-sdk/proxywasm/types"
)

func main() {
  proxywasm.SetVMContext(&vmContext{})
}

type vmContext struct {
  // Embed the default VM context here,
  // so that we don't need to reimplement all the methods.
  types.DefaultVMContext
}

// Override types.DefaultVMContext.
func (*vmContext) NewPluginContext(contextID uint32) types.PluginContext {
  return &pluginContext{}
}

type pluginContext struct {
  // Embed the default plugin context here,
  // so that we don't need to reimplement all the methods.
  types.DefaultPluginContext
}

// Override types.DefaultPluginContext.
func (*pluginContext) NewHttpContext(contextID uint32) types.HttpContext {
  return &httpHeaders{contextID: contextID}
}

type httpHeaders struct {
  // Embed the default http context here,
  // so that we don't need to reimplement all the methods.
  types.DefaultHttpContext
  contextID uint32
}

func (ctx *httpHeaders) OnHttpRequestHeaders(numHeaders int, endOfStream bool) types.Action {
  proxywasm.LogInfo("OnHttpRequestHeaders")
  return types.ActionContinue
}

func (ctx *httpHeaders) OnHttpResponseHeaders(numHeaders int, endOfStream bool) types.Action {
  proxywasm.LogInfo("OnHttpResponseHeaders")
  return types.ActionContinue
}

func (ctx *httpHeaders) OnHttpStreamDone() {
  proxywasm.LogInfof("%d finished", ctx.contextID)
}
