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

## Limitation
Currently, it is necessary to pass in the `uuid` for `channel`, `planType`, `generation` and `region` of a Camunda cluster. See example below. 

## Examples

Example of a created cluster object
```yaml
apiVersion: camunda.crossplane.io/v1alpha1
kind: Cluster
metadata:
  annotations:
    ...
    crossplane.io/external-name: 2611e047-74ab-47ba-aae4-115be2918fbe
  name: my-camunda-cluster-123
spec:
  deletionPolicy: Delete
  forProvider:
    channel: 6bdf0d1c-3d5a-4df6-8d03-762682964d85
    generation: d54fde93-275f-480d-a7b4-bc52435a447a
    planType: 231932af-0223-4b60-9961-fe4f71800760
    region: 2f6470f9-77ec-4be5-9cdc-3231caf683ec
  providerConfigRef:
    name: example
  writeConnectionSecretToRef:
    name: my-cluster-details
    namespace: default
status:
  atProvider:
    operate: https://bru-2.operate.camunda.io/2611e047-74ab-47ba-aae4-115be2918fbe
    optimize: https://bru-2.optimize.camunda.io/2611e047-74ab-47ba-aae4-115be2918fbe
    tasklist: https://bru-2.tasklist.camunda.io/2611e047-74ab-47ba-aae4-115be2918fbe
    zeebe: 2611e047-74ab-47ba-aae4-115be2918fbe.bru-2.zeebe.camunda.io
  conditions:
    ...
```

Example of a created client object
```yaml
apiVersion: camunda.crossplane.io/v1alpha1
kind: Client
metadata:
  annotations:
    ...
    crossplane.io/external-name: 31~Bt1k3tre00cgGUKDW7XYlX3neKTxd
  name: my-camunda-zeebe-client
spec:
  deletionPolicy: Delete
  forProvider:
    clusterID: 2611e047-74ab-47ba-aae4-115be2918fbe
  providerConfigRef:
    name: example
  writeConnectionSecretToRef:
    name: my-client-details
    namespace: default
status:
  atProvider:
    zeebeAddress: 2611e047-74ab-47ba-aae4-115be2918fbe.bru-2.zeebe.camunda.io:443
    zeebeAuthorizationServerUrl: https://login.cloud.camunda.io/oauth/token
    zeebeClientID: 31~Bt1k3tre00cgGUKDW7XYlX3neKTxd
  conditions:
    ...
```

The example resources are located in the `examples` folder. 

## Developing

1. Run `make` to initialize the "build" Make submodule we use for CI/CD.
1. Run `make reviewable` to run code generation, linters, and tests.

Refer to Crossplane's [CONTRIBUTING.md] file for more information on how the
Crossplane community prefers to work. The [Provider Development][provider-dev]
guide may also be of use.

[CONTRIBUTING.md]: https://github.com/crossplane/crossplane/blob/master/CONTRIBUTING.md
[provider-dev]: https://github.com/crossplane/crossplane/blob/master/docs/contributing/provider_development_guide.md
