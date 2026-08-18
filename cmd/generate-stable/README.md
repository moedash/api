# Stable API Plugin

`protoc-gen-stable-api` generates the stable set of proto definitions from the
full `api_next` schema. It uses descriptors for filtering and `protoprint` to
emit the resulting `.proto` files.

`api_next` is the source of truth. `api` is generated output and must not be
manually edited.

Run the complete generation flow from the repository root:

```bash
make stable-api
```

The Make target builds this plugin, runs it through Buf using
`buf.stable.gen.yaml`, and formats the generated stable tree.
