.ONESHELL:
.SILENT:

# The web package shares the repo version (one VERSION for the whole repo).
WEB_VERSION := $(VERSION)

WEB_PKG_ROOT     := out/web
WEB_CONTROL_ROOT := $(WEB_PKG_ROOT)/control
WEB_DATA_ROOT    := $(WEB_PKG_ROOT)/data
WEB_WWW          := $(WEB_DATA_ROOT)/opt/share/www/usque
WEB_FILENAME     := usque-keenetic-web_$(WEB_VERSION)_all_entware.ipk

WEB_DEPENDS := usque-keenetic, php8-cgi, php8-mod-session, php8-mod-curl, curl, lighttpd, lighttpd-mod-cgi, lighttpd-mod-setenv

web:
	set -e
	echo "Build web package $(WEB_VERSION)..."
	rm -rf $(WEB_PKG_ROOT)
	mkdir -p $(WEB_CONTROL_ROOT)
	mkdir -p $(WEB_WWW)/api
	mkdir -p $(WEB_DATA_ROOT)/opt/etc/lighttpd/conf.d
	mkdir -p $(WEB_DATA_ROOT)/opt/etc
	mkdir -p out/tmp

	# --- data ---
	cp web/public/index.html   $(WEB_WWW)/index.html
	cp web/public/app.js       $(WEB_WWW)/app.js
	cp web/public/style.css    $(WEB_WWW)/style.css
	cp web/public/favicon.svg  $(WEB_WWW)/favicon.svg
	cp web/backend/index.php   $(WEB_WWW)/api/index.php
	cp web/openapi.yaml        $(WEB_WWW)/openapi.yaml
	echo "$(WEB_VERSION)" > $(WEB_WWW)/version
	cp web/lighttpd/81-usque.conf $(WEB_DATA_ROOT)/opt/etc/lighttpd/conf.d/81-usque.conf
	cp web/conf/usque_web.conf    $(WEB_DATA_ROOT)/opt/etc/usque_web.conf

	# --- control ---
	{
		echo "Package: usque-keenetic-web"
		echo "Version: $(WEB_VERSION)"
		echo "Depends: $(WEB_DEPENDS)"
		echo "License: MIT"
		echo "Section: net"
		echo "URL: https://github.com/side-effect-tm/usque-keenetic"
		echo "Architecture: all"
		echo "Description: usque-keenetic web interface (monitoring & control)"
		echo ""
	} > $(WEB_CONTROL_ROOT)/control
	cp scripts/ipk-web/conffiles $(WEB_CONTROL_ROOT)/conffiles
	cp scripts/ipk-web/postinst  $(WEB_CONTROL_ROOT)/postinst
	cp scripts/ipk-web/prerm     $(WEB_CONTROL_ROOT)/prerm
	cp scripts/ipk-web/postrm    $(WEB_CONTROL_ROOT)/postrm
	chmod +x $(WEB_CONTROL_ROOT)/postinst $(WEB_CONTROL_ROOT)/prerm $(WEB_CONTROL_ROOT)/postrm

	# --- pack ipk ---
	( cd $(WEB_CONTROL_ROOT) && tar czf ../control.tar.gz . )
	( cd $(WEB_DATA_ROOT) && tar czf ../data.tar.gz . )
	echo 2.0 > $(WEB_PKG_ROOT)/debian-binary
	( cd $(WEB_PKG_ROOT) && tar czf ../tmp/$(WEB_FILENAME) control.tar.gz data.tar.gz debian-binary )
	echo "  -> out/tmp/$(WEB_FILENAME)"
