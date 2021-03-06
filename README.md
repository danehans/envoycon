# Developing Envoy Wasm Extensions

We will be using the [proxy-wasm-go-sdk](https://github.com/tetratelabs/proxy-wasm-go-sdk) to build and test an Envoy
Wasm extension. Then we'll show a way to configure the Wasm module using the EnvoyFilter resource and deploy it to Envoy
sidecars in a Kubernetes cluster.

We'll start with something trivial for our first example and write a simple Wasm module using TinyGo that adds a custom
header to response headers.

## Prerequisites

- [Go](https://golang.org/doc/install) 1.17 or greater
- [TinyGo](https://tinygo.org/getting-started/install/)
- [Envoy](https://www.envoyproxy.io/docs/envoy/latest/start/install)

## Bootstrap the project

### Option 1: Use the demo repo

Clone the demo repo:
```shell
git clone https://github.com/danehans/envoycon.git && cd envoycon/demo/header-filter
```

### Option 2: From scratch

Create a directory for our extension, initialize the Go module, and download the SDK dependency:

```sh
$ mkdir header-filter && cd header-filter
$ go mod init header-filter
$ go mod edit -require=github.com/tetratelabs/proxy-wasm-go-sdk@main
$ go mod download github.com/tetratelabs/proxy-wasm-go-sdk
```

## Create the Wasm module

Next, let's create the `main.go` file where the code for our WASM extension will live:

```go
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
```

Save the above contents to a file called `main.go`.

Let's build the filter to check everything is good:

```sh
tinygo build -o main.wasm -scheduler=none -target=wasi main.go
```

The build command should run successfully, and it should generate a file called `main.wasm`.

Run a local Envoy instance to test the extension we've built. First, we need an Envoy config that will configure the
extension:

```yaml
static_resources:
  listeners:
    - name: main
      address:
        socket_address:
          address: 0.0.0.0
          port_value: 10000
      filter_chains:
        - filters:
            - name: envoy.http_connection_manager
              typed_config:
                "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
                stat_prefix: ingress_http
                route_config:
                  name: local_route
                  virtual_hosts:
                    - name: local_service
                      domains: ["*"]
                      routes:
                        - match:
                            prefix: "/"
                          direct_response:
                            status: 200
                            body:
                              inline_string: "Hello world"
                http_filters:
                  - name: envoy.filters.http.wasm
                    typed_config:
                      "@type": type.googleapis.com/udpa.type.v1.TypedStruct
                      type_url: type.googleapis.com/envoy.extensions.filters.http.wasm.v3.Wasm
                      value:
                        config:
                          vm_config:
                            runtime: "envoy.wasm.runtime.v8"
                            code:
                              local:
                                filename: "main.wasm"
                  - name: envoy.filters.http.router

admin:
  access_log_path: "/dev/null"
  address:
    socket_address:
      address: 0.0.0.0
      port_value: 8001
```

Save the above to `envoy.yaml` file.

The Envoy configuration sets up a single listener on port 10000 that returns a direct response (HTTP 200) with body
`Hello World`. Inside the `http_filters` section, we're configuring the `envoy.filters.http.wasm` filter and referencing
the local WASM file (`main.wasm`) we've built earlier.

Let's run Envoy with this configuration:

```sh
envoy -c envoy.yaml --concurrency 2 --log-format '%v'
```

In a separate terminal window, send a request to the port Envoy is listening on (`10000`):

```sh
$ curl http://envoycon.daneyon.com:10000
Hello World
```

__Note:__ "envoycon.daneyon.com" resolves to 127.0.0.1.

Envoy should log the following:
```sh
wasm log: OnHttpRequestHeaders
wasm log: OnHttpResponseHeaders
wasm log: 2 finished
```

The output shows the three log entries - one from the `OnHttpRequestHeaders()` handler and the second one from the
`OnHttpResponseHeaders()` handler. The last line is from `OnHttpStreamDone()`, indicating the filter is done processing
and logs the context ID.

Stop the Envoy process by pressing CTRL+C.

## Setting additional headers on HTTP response

Let's open the `main.go` file and add a header to the response headers.

We'll call the `AddHttpResponseHeader` function to add a new header. Update the `OnHttpResponseHeaders` function to look
like this:

```go
func (ctx *httpHeaders) OnHttpResponseHeaders(numHeaders int, endOfStream bool) types.Action {
  proxywasm.LogInfo("OnHttpResponseHeaders")
  err := proxywasm.AddHttpResponseHeader("my-new-header", "some-value-here")
  if err != nil {
    proxywasm.LogCriticalf("failed to add response header: %v", err)
  }
  return types.ActionContinue
}
```

Let's rebuild the extension:

```sh
tinygo build -o main.wasm -scheduler=none -target=wasi main.go
```

And we can now re-run Envoy with the updated extension:

```sh
envoy -c envoy.yaml --concurrency 2 --log-format '%v'
```

Now, if we send a request again (make sure to add the `-v` flag), we'll see the header that was added to the response:

```sh
$ curl -v http://envoycon.daneyon.com:10000
...
< my-new-header: some-value-here
...
Hello World
```

## Reading values from configuration

Hard-coding values like that in code is never a good idea. Let's see how we can read the additional headers.

Add the `additionalHeaders` and `contextID` to the `pluginContext` struct:

  ```go
  type pluginContext struct {
    // Embed the default plugin context here,
    // so that we don't need to reimplement all the methods.
    types.DefaultPluginContext
    additionalHeaders map[string]string
    contextID         uint32
  }
  ```

Update the `NewPluginContext` function to initialize the values:

  ```go
  func (*vmContext) NewPluginContext(contextID uint32) types.PluginContext {
    return &pluginContext{contextID: contextID, additionalHeaders: map[string]string{}}
  }
  ```

In the `OnPluginStart` function we can now read in values from the Envoy configuration and store the key/value pairs in
the `additionalHeaders` map:

  ```go
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
  ```

To access the configuration values we've set, we need to add the map to the HTTP context when we initialize it. To do
that, we need to update the `httpheaders` struct first:

```go
type httpHeaders struct {
  // Embed the default http context here,
  // so that we don't need to reimplement all the methods.
  types.DefaultHttpContext
  contextID         uint32
  additionalHeaders map[string]string
}
```

Then, in the `NewHttpContext` function we can instantiate the httpHeaders with the additional headers map coming from
the plugin context:

```go
func (ctx *pluginContext) NewHttpContext(contextID uint32) types.HttpContext {
  return &httpHeaders{contextID: contextID, additionalHeaders: ctx.additionalHeaders}
}
```

Finally, in order to set the headers we modify the `OnHttpResponseHeaders` function, iterate through the
`additionalHeaders` map and call the `AddHttpResponseHeader` for each item:

```go
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
```

Let's rebuild the extension again:

```sh
tinygo build -o main.wasm -scheduler=none -target=wasi main.go
```

Also, let's update the config file to include additional headers in the filter configuration:

```yaml
- name: envoy.filters.http.wasm
  typed_config:
    "@type": type.googleapis.com/udpa.type.v1.TypedStruct
    type_url: type.googleapis.com/envoy.extensions.filters.http.wasm.v3.Wasm
    value:
      config:
        vm_config:
          runtime: "envoy.wasm.runtime.v8"
          code:
            local:
              filename: "main.wasm"
        # ADD THESE LINES
        configuration:
          "@type": type.googleapis.com/google.protobuf.StringValue
          value: |
            header_1=somevalue
            header_2=secondvalue
```

With the filter updated, we can re-run the proxy again. When you send a request, you'll notice the headers we set in the
filter configuration are added as response headers:

```sh
$ curl -v http://envoycon.daneyon.com:10000
...
< header_1: somevalue
< header_2: secondvalue
```

## Add a metric

Let's add another feature - a counter that increases each time there's a request header called `hello` set.

First, let's update the `pluginContext` to include the `helloHeaderCounter`:

```go
type pluginContext struct {
  // Embed the default plugin context here,
  // so that we don't need to reimplement all the methods.
  types.DefaultPluginContext
  additionalHeaders  map[string]string
  contextID          uint32
  // ADD THIS LINE
  helloHeaderCounter proxywasm.MetricCounter
}
```

With the metric counter in the struct, we can now create it in the `NewPluginContext` function. We'll call the header
`hello_header_counter`.

```go
func (*vmContext) NewPluginContext(contextID uint32) types.PluginContext {
  return &pluginContext{contextID: contextID, additionalHeaders: map[string]string{}, helloHeaderCounter: proxywasm.DefineCounterMetric("hello_header_counter")}
}
```

Since we want need to check the incoming request headers to decide whether to increment the counter, we need to add the
`helloHeaderCounter` to the `httpHeaders` struct as well:

```go
type httpHeaders struct {
  // Embed the default http context here,
  // so that we don't need to reimplement all the methods.
  types.DefaultHttpContext
  contextID          uint32
  additionalHeaders  map[string]string
  // ADD THIS LINE
  helloHeaderCounter proxywasm.MetricCounter
}
```

Also, we need to get the counter from the `pluginContext` and set it when we're creating the new HTTP context:

```go
// Override types.DefaultPluginContext.
func (ctx *pluginContext) NewHttpContext(contextID uint32) types.HttpContext {
  return &httpHeaders{contextID: contextID, additionalHeaders: ctx.additionalHeaders, helloHeaderCounter: ctx.helloHeaderCounter}
}
```

Now that we've piped the `helloHeaderCounter` all the way through to the `httpHeaders`, we can use it in the
`OnHttpRequestHeaders` function:

```go
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
```

We're checking if the "hello" request header is defined (note that we don't care about the header value), and if
it's defined, we call the `Increment` function on the counter instance. Otherwise, we'll ignore it and return
ActionContinue if we get an error from the `GetHttpRequestHeader` call.

Let's rebuild the extension again:

```sh
tinygo build -o main.wasm -scheduler=none -target=wasi main.go
```

And then re-run the Envoy proxy. Make a couple of requests like this:

```sh
curl -H "hello: something" http://envoycon.daneyon.com:10000
```

The Envoy log should include the following message:

```text
wasm log: hello_header_counter incremented
```

You can also use the admin address on port 8001 to check that the metric is being tracked:

```sh
$ curl localhost:8001/stats/prometheus | grep hello
# TYPE envoy_hello_header_counter counter
envoy_hello_header_counter{} 1
```

## Deploying Wasm module to Istio using EnvoyFilter

You can skip the prerequisites if you are using an existing Istio mesh (v1.9 or greater).

### Prerequisites

- [Kind](https://kind.sigs.k8s.io/docs/user/quick-start/)
- [Kubectl](https://v1-18.docs.kubernetes.io/docs/tasks/tools/install-kubectl/)
- [Istioctl](https://istio.io/latest/docs/setup/getting-started/#download)

Create the Kubernetes cluster using the provided `demo/hack/kind.sh` script. Note that the script maps host ports 80/443
to Istio ingress gateway container ports 30080/30443.

Install the Istio operator:
```shell
istioctl operator init
```

After the operator is running, install Istio using the provided operator manifest:
```shell
kubectl apply -f demo/manifests/operator.yaml
```

Enable automatic Istio sidecar injection for the default namespace:
```shell
kubectl label ns/default istio-injection=enabled
```

The EnvoyFilter resource is used to deploy a Wasm module to Envoy proxies for Istio. EnvoyFilter gives us the ability to
customize the Envoy configuration. It allows us to modify values, configure new listeners or clusters, and add filters.

In the previous example, there was no need to push or publish the `main.wasm` file anywhere, as it was accessible by the
Envoy proxy because everything was running locally. However, now that we want to run the Wasm module in Envoy proxies
that are part of the Istio service mesh, we need to make the `main.wasm` file available to all those proxies so they can
load and run it.

Since Envoy can be extended using filters, we can use the Envoy HTTP Wasm filter to implement an HTTP filter with a Wasm
module. This filter allows us to configure the Wasm module and load the module file.

Here's a snippet that shows how to load a Wasm module using the Envoy HTTP Wasm filter:

```yaml
name: envoy.filters.http.wasm
typed_config:
  "@type": type.googleapis.com/envoy.extensions.filters.http.wasm.v3.Wasm
  config:
    config:
      name: "my_plugin"
      vm_config:
        runtime: "envoy.wasm.runtime.v8"
        code:
          local:
            filename: "/etc/envoy_filter_http_wasm_example.wasm"
        allow_precompiled: true
    configuration:
       '@type': type.googleapis.com/google.protobuf.StringValue
       value: |
         {}
```

This particular snippet is reading the Wasm file from the local path. Note that "local" in this case refers to the
container the Envoy proxy is running in.

One way we could bring the Wasm module to that container is to use a persistent volume, for example. We'd then copy the
Wasm file to the persistent disk and use the following annotations to mount the volume into the Envoy proxy sidecars:

```yaml
sidecar.istio.io/userMount: '[{"name": "wasmfilters", "mountPath": "/wasmfilters"}]'
sidecar.istio.io/userVolume: '[{"name": "wasmfilters", "gcePersistentDisk": { "pdName": "my-data-disk", "fsType": "ext4" }}]'
```

Note that the above snippet assumes a persistent disk. The disk could be any other persistent volume as well. We'd then
have to patch the existing Kubernetes deployments and 'inject' the above annotations.

Luckily for us, there is another option. Remember the local field from the Envoy HTTP Wasm filter configuration? Well,
there's also a remote field we can use to load the Wasm module from a remote location, a URL. The remote field
simplifies things a lot! We can upload the .wasm file to remote storage, get the public URL to the module, and then use
it.

In this example, the module has been uploaded to my GitHub repo.

We can now create the EnvoyFilter resource that tells Envoy where to download the extension as well as where to inject
it:
```yaml
apiVersion: networking.istio.io/v1alpha3
kind: EnvoyFilter
metadata:
  name: headers-extension
spec:
  configPatches:
  - applyTo: EXTENSION_CONFIG
    patch:
      operation: ADD
      value:
        name: headers-extension
        typed_config:
          "@type": type.googleapis.com/udpa.type.v1.TypedStruct
          type_url: type.googleapis.com/envoy.extensions.filters.http.wasm.v3.Wasm
          value:
            config:
              vm_config:
                vm_id: headers-extension-vm
                runtime: envoy.wasm.runtime.v8
                code:
                  remote:
                    http_uri:
                      uri: https://github.com/danehans/envoycon/blob/main/demo/header-filter/main.wasm?raw=true
              configuration:
                "@type": type.googleapis.com/google.protobuf.StringValue
                value: |
                  header_1=somevalue
                  header_2=secondvalue
  - applyTo: HTTP_FILTER
    match:
      context: SIDECAR_INBOUND
      listener:
        filterChain:
          filter:
            name: envoy.filters.network.http_connection_manager
    patch:
      operation: INSERT_BEFORE
      value:
        name: headers-extension
        config_discovery:
          config_source:
            ads: {}
            initial_fetch_timeout: 0s # wait indefinitely to prevent bad Wasm fetch
          type_urls: [ "type.googleapis.com/envoy.extensions.filters.http.wasm.v3.Wasm"]
```

Note that we're deploying the EnvoyFilters to the default namespace. We could also deploy them to a root namespace
(e.g. `istio-system`) if we wanted to apply the filter to all workloads in the mesh. Additionally, we could specify the
selectors to pick the workloads to which we want to apply the filter.

Save the above YAML to envoyfilter.yaml file and create it:

```sh
$ kubectl apply -f envoyfilter.yaml
envoyfilter.networking.istio.io/headers-extension created
```

To try out the module, you can deploy a sample workload. I'm using this httpbin example:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: httpbin
---
apiVersion: v1
kind: Service
metadata:
  name: httpbin
  labels:
    app: httpbin
    service: httpbin
spec:
  ports:
  - name: http
    port: 8000
    targetPort: 80
  selector:
    app: httpbin
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: httpbin
spec:
  replicas: 1
  selector:
    matchLabels:
      app: httpbin
      version: v1
  template:
    metadata:
      labels:
        app: httpbin
        version: v1
    spec:
      serviceAccountName: httpbin
      containers:
      - image: docker.io/kennethreitz/httpbin
        imagePullPolicy: IfNotPresent
        name: httpbin
        ports:
        - containerPort: 80
```

Save the above file to httpbin.yaml and deploy it using `kubectl apply -f httpbin.yaml`.

Before continuing, check that the httpbin is running and the Envoy sidecar was injected:

```sh
$ kubectl get po
NAME                       READY   STATUS        RESTARTS   AGE
httpbin-66cdbdb6c5-4pv44   2/2     Running       1          11m
```

To see if something went wrong with downloading the Wasm module, you can look at the Istiod logs:
```sh
kubectl logs deploy/istiod -n istio-system
```

Let's try out the deployed Wasm module!

### Option 1: Internal
We will create a single Pod inside the cluster, and from there, we will send a request to `http://httpbin:8000/get`

```sh
$ kubectl run curl --image=curlimages/curl -it --rm -- /bin/sh
Defaulted container "curl" out of: curl, istio-proxy, istio-init (init)
If you don't see a command prompt, try pressing enter.
/ $
```

Once you get the prompt to the curl container, send a request to the `httpbin` service:

```sh
/ $ curl -v http://httpbin:8000/headers
...
< header_1: somevalue
< header_2: secondvalue
```

### Option 2: External

Since the example httpbin includes an Istio Gateway and VirtualService, you can your Wasm module externally:
```shell
$ curl -v http://envoycon.daneyon.com/headers
< header_1: somevalue
< header_2: secondvalue
...
```

Notice the two headers we defined in the Wasm module are being set in the response.

## Cleanup

To delete all created resources from your cluster, run the following:

```sh
kubectl delete envoyfilter headers-extension
kubectl delete deploy httpbin
kubectl delete svc httpbin
kubectl delete sa httpbin
kubectl delete vs httpbin
kubectl delete gw httpbin
```
