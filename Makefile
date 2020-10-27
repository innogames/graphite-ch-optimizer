NAME = graphite-ch-optimizer
VERSION = $(shell git describe --long --tags 2>/dev/null | sed 's/^v//;s/\([^-]*-g\)/c\1/;s/-/./g')
VENDOR = "System Administration <it@innogames.com>"
URL = https://github.com/innogames/$(NAME)
define DESC =
'Service to optimize stale GraphiteMergeTree tables
 This software looking for tables with GraphiteMergeTree engine and evaluate if some of partitions should be optimized. It could work both as one-shot script and background daemon.'
endef
GO_FILES = $(shell find -name '*.go')
PKG_FILES = build/$(NAME)_$(VERSION)_amd64.deb build/$(NAME)-$(VERSION)-1.x86_64.rpm
SUM_FILES = build/sha256sum build/md5sum
MODULE = github.com/innogames/$(NAME)


.PHONY: all clean docker test version

all: build

version:
	@echo $(VERSION)

clean:
	rm -rf artifact
	rm -rf build
	rm -rf $(NAME)

rebuild: clean all

# Run tests
test:
	$(GO) vet $(MODULE)
	$(GO) test $(MODULE)

build: | $(NAME)
	mkdir build

docker:
	docker build -t innogames/$(NAME):builder -f docker/builder/Dockerfile .
	docker build -t innogames/$(NAME):latest -f docker/$(NAME)/Dockerfile .

$(NAME): $(NAME).go
	go build -ldflags "-X 'main.version=$(VERSION)'" -o $@ .

#########################################################
# Prepare artifact directory and set outputs for upload #
#########################################################
github_artifact: $(foreach art,$(PKG_FILES) $(SUM_FILES), artifact/$(notdir $(art)))

artifact:
	mkdir $@

# Link artifact to directory with setting step output to filename
artifact/%: ART=$(notdir $@)
artifact/%: TYPE=$(lastword $(subst ., ,$(ART)))
artifact/%: build/% | artifact
	cp -l $< $@
	@echo '::set-output name=$(TYPE)::$(ART)'

#######
# END #
#######

#############
# Packaging #
#############

# Prepare everything for packaging
.ONESHELL:
build/pkg: build/$(NAME) build/config.toml.example
	cd build
	mkdir -p pkg/etc/$(NAME)
	mkdir -p pkg/usr/bin
	cp -l $(NAME) pkg/usr/bin/
	cp -l config.toml.example pkg/etc/$(NAME)

build/$(NAME): $(NAME).go
	GOOS=linux GOARCH=amd64 go build -ldflags "-X 'main.version=$(VERSION)'" -o $@ .

build/config.toml.example: build/$(NAME)
	./build/$(NAME) --print-defaults > $@

packages: $(PKG_FILES) $(SUM_FILES)

# md5 and sha256 sum-files for packages
$(SUM_FILES): COMMAND = $(notdir $@)
$(SUM_FILES): PKG_FILES_NAME = $(notdir $(PKG_FILES))
.ONESHELL:
$(SUM_FILES): $(PKG_FILES)
	cd build
	$(COMMAND) $(PKG_FILES_NAME) > $(COMMAND)

deb: $(word 1, $(PKG_FILES))

rpm: $(word 2, $(PKG_FILES))

# Set TYPE to package suffix w/o dot
$(PKG_FILES): TYPE = $(subst .,,$(suffix $@))
$(PKG_FILES): build/pkg
	fpm --verbose \
		-s dir \
		-a x86_64 \
		-t $(TYPE) \
		--vendor $(VENDOR) \
		-m $(VENDOR) \
		--url $(URL) \
		--description $(DESC) \
		--license MIT \
		-n $(NAME) \
		-v $(VERSION) \
		--after-install packaging/postinst \
		--before-remove packaging/prerm \
		-p build \
		build/pkg/=/ \
		packaging/$(NAME).service=/lib/systemd/system/$(NAME).service

#######
# END #
#######
