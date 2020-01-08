NAME = graphite-ch-optimizer
VERSION = $(shell git describe --long --tags 2>/dev/null | sed 's/^v//;s/\([^-]*-g\)/c\1/;s/-/./g')
VENDOR = "System Administration <it@innogames.com>"
URL = https://github.com/innogames/$(NAME)
define DESC =
'Service to optimize stale GraphiteMergeTree tables
 This software looking for tables with GraphiteMergeTree engine and evaluate if some of partitions should be optimized. It could work both as one-shot script and background daemon.'
endef
PKG_FILES = build/$(NAME)_$(VERSION)_amd64.deb build/$(NAME)-$(VERSION)-1.x86_64.rpm

.PHONY: clean all

all: build

clean:
	rm -rf build
	rm -rf $(NAME)

rebuild: clean all

build: $(NAME)

$(NAME):
	go build -ldflags "-X 'main.version=$(VERSION)'" -o $@ .

build/$(NAME): $(NAME).go
	GOOS=linux GOARCH=amd64 go build -ldflags "-X 'main.version=$(VERSION)'" -o $@ .

build/config.toml.example: build/$(NAME)
	./build/$(NAME) --print-defaults > $@

packages: $(PKG_FILES)

.ONESHELL:
build/pkg: build/$(NAME) build/config.toml.example
	cd build
	mkdir -p pkg/etc/$(NAME)
	mkdir -p pkg/usr/bin
	cp -l $(NAME) pkg/usr/bin/
	cp -l config.toml.example pkg/etc/$(NAME)

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
