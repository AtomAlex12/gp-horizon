VERSION := $(shell cat VERSION)
USQUE_VERSION := $(shell cat USQUE_VERSION)
ROOT_DIR := /opt

include repository.mk
include packages.mk
include web.mk

.DEFAULT_GOAL := packages

# core packages + web package
packages: pkg-all web

clean:
	rm -rf out/
