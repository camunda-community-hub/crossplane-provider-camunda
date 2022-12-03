# Crossplane Provider Camunda

A crossplane provider for Camunda Platform 8 SaaS. https://camunda.com/

## Creating the provider secret

echo '{ "client_id": "<your_client_id", "client_secret": "your_client_secret" }' | base64

```yaml
apiVersion: v1
kind: Secret
metadata:
  namespace: crossplane-system
  name: example-provider-secret
type: Opaque
data:
  credentials: <your_base64_encoded_string>
```

## Developing

1. Run `make` to initialize the "build" Make submodule we use for CI/CD.
1. Run `make reviewable` to run code generation, linters, and tests.

Refer to Crossplane's [CONTRIBUTING.md] file for more information on how the
Crossplane community prefers to work. The [Provider Development][provider-dev]
guide may also be of use.

[CONTRIBUTING.md]: https://github.com/crossplane/crossplane/blob/master/CONTRIBUTING.md
[provider-dev]: https://github.com/crossplane/crossplane/blob/master/docs/contributing/provider_development_guide.md
