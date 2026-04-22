RELEASE_SCRIPT ?= ./scripts/release.sh

REL_CMD  ?= goreleaser
DIST_DIR ?= ./dist
SRCDIR   ?= .

# Image tag: use goreleaser-style version if on a tag, else branch-sha.
VERSION  ?= $(shell ./tools/image-tag | cut -d, -f 1)
IMG_NAME ?= iotcontroller

# Example usage: make release version=0.11.0
release: build
	@echo "=== $(PROJECT_NAME) === [ release          ]: Generating release."
	git fetch --tags
	$(REL_CMD) release

release-clean:
	@echo "=== $(PROJECT_NAME) === [ release-clean    ]: distribution files..."
	@rm -rfv $(DIST_DIR) $(SRCDIR)/tmp

release-publish: clean tools docker-login release-notes
	@echo "=== $(PROJECT_NAME) === [ release-publish  ]: Publishing release via $(REL_CMD)"
	$(REL_CMD) --release-notes=$(SRCDIR)/tmp/$(RELEASE_NOTES_FILE)

# Local Snapshot
snapshot: release-clean
	@echo "=== $(PROJECT_NAME) === [ snapshot         ]: Creating release via $(REL_CMD)"
	@echo "=== $(PROJECT_NAME) === [ snapshot         ]:   THIS WILL NOT BE PUBLISHED!"
	$(REL_CMD) --skip=publish --snapshot

# Build and push a docker image to a registry.
# Usage: make ci-docker registry=reg.dist.svc.cluster.znet:5000
ci-docker:
	@if [ -z "$(registry)" ]; then echo "registry is required: make ci-docker registry=<host:port>"; exit 1; fi
	@echo "=== $(PROJECT_NAME) === [ ci-docker        ]: Building $(registry)/$(IMG_NAME):$(VERSION)"
	docker build \
		--build-arg TARGETOS=linux \
		--build-arg TARGETARCH=amd64 \
		-t $(registry)/$(IMG_NAME):$(VERSION) \
		-t $(registry)/$(IMG_NAME):latest \
		.
	@echo "=== $(PROJECT_NAME) === [ ci-docker        ]: Pushing $(registry)/$(IMG_NAME):$(VERSION)"
	docker push $(registry)/$(IMG_NAME):$(VERSION)
	docker push $(registry)/$(IMG_NAME):latest

.PHONY: release release-clean release-publish snapshot ci-docker
