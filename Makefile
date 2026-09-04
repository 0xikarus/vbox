VMBOX_IMAGE ?= vmbox-box:local
VMBOX_COMPONENTS ?= codex,claude,opencode,bun,foundry
VMBOX_PLATFORM ?= linux/amd64
VMBOX_IMAGE_VERSION ?= dev

.PHONY: box-image box-image-push

# Build a preloaded workload image into the local Docker image store.
box-image:
	docker build --platform "$(VMBOX_PLATFORM)" --build-arg "VMBOX_COMPONENTS=$(VMBOX_COMPONENTS)" --build-arg "VMBOX_IMAGE_VERSION=$(VMBOX_IMAGE_VERSION)" -t "$(VMBOX_IMAGE)" .

# Build and publish a Railway-pullable image. VMBOX_IMAGE must be a registry tag.
box-image-push:
	docker buildx build --platform "$(VMBOX_PLATFORM)" --build-arg "VMBOX_COMPONENTS=$(VMBOX_COMPONENTS)" --build-arg "VMBOX_IMAGE_VERSION=$(VMBOX_IMAGE_VERSION)" -t "$(VMBOX_IMAGE)" --push .
