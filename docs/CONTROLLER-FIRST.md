# Controller-first CLI

The normal `vmbox` CLI connects to a controller. It does not bootstrap a
provider or fall back to a standalone deployment. Provider bootstrap is an
explicit operator-only action.

For current setup and operations, see [controller operations](CONTROLLER.md).
For worker choices and provider configuration, see [worker providers](PROVIDERS.md).
The API contract is in [OpenAPI](openapi.yaml).
