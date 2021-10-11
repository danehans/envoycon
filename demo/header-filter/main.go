package main

import (
  "bufio"
  "bytes"
  "github.com/tetratelabs/proxy-wasm-go-sdk/proxywasm"
  "github.com/tetratelabs/proxy-wasm-go-sdk/proxywasm/types"
  "strings"
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
  return &pluginContext{
    contextID:          contextID,
    additionalHeaders:  map[string]string{},
    helloHeaderCounter: proxywasm.DefineCounterMetric("hello_header_counter"),
  }
}

type pluginContext struct {
  // Embed the default plugin context here,
  // so that we don't need to reimplement all the methods.
  types.DefaultPluginContext
  additionalHeaders  map[string]string
  contextID          uint32
  helloHeaderCounter proxywasm.MetricCounter
}

func (ctx *pluginContext) OnPluginStart(pluginConfigurationSize int) types.OnPluginStartStatus {
  // Get the plugin configuration
  config, err := proxywasm.GetPluginConfiguration()
  if err != nil && err != types.ErrorStatusNotFound {
    proxywasm.LogCriticalf("failed to load config: %v", err)
    return types.OnPluginStartStatusFailed
  }

  // Read the config
  scanner := bufio.NewScanner(bytes.NewReader(config))
  for scanner.Scan() {
    line := scanner.Text()
    if strings.HasPrefix(line, "#") {
      continue
    }
    // Each line in the config is in the "key=value" format
    if tokens := strings.Split(scanner.Text(), "="); len(tokens) == 2 {
      ctx.additionalHeaders[tokens[0]] = tokens[1]
    }
  }
  return types.OnPluginStartStatusOK
}

// Override types.DefaultPluginContext.
func (ctx *pluginContext) NewHttpContext(contextID uint32) types.HttpContext {
  return &httpHeaders{
    contextID:          contextID,
    additionalHeaders:  ctx.additionalHeaders,
    helloHeaderCounter: ctx.helloHeaderCounter,
  }
}

type httpHeaders struct {
  // Embed the default http context here,
  // so that we don't need to reimplement all the methods.
  types.DefaultHttpContext
  contextID          uint32
  additionalHeaders  map[string]string
  helloHeaderCounter proxywasm.MetricCounter

}

func (ctx *httpHeaders) OnHttpRequestHeaders(numHeaders int, endOfStream bool) types.Action {
  proxywasm.LogInfo("OnHttpRequestHeaders")

  _, err := proxywasm.GetHttpRequestHeader("hello")
  if err != nil {
    // Ignore if header is not set
    return types.ActionContinue
  }

  ctx.helloHeaderCounter.Increment(1)
  proxywasm.LogInfo("hello_header_counter incremented")
  return types.ActionContinue
}

func (ctx *httpHeaders) OnHttpResponseHeaders(numHeaders int, endOfStream bool) types.Action {
  proxywasm.LogInfo("OnHttpResponseHeaders")

  for key, value := range ctx.additionalHeaders {
    if err := proxywasm.AddHttpResponseHeader(key, value); err != nil {
      proxywasm.LogCriticalf("failed to add header: %v", err)
      return types.ActionPause
    }
    proxywasm.LogInfof("header set: %s=%s", key, value)
  }

  return types.ActionContinue
}

func (ctx *httpHeaders) OnHttpStreamDone() {
  proxywasm.LogInfof("%d finished", ctx.contextID)
}
