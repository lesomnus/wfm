FROM ghcr.io/lesomnus/dev-golang:1.26

# nmcli (NetworkManager backend) + dbus to reach the host NM over its system bus,
# and iproute2 for the `ip` calls the nm backend shells out to.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
	--mount=type=cache,target=/var/lib/apt,sharing=locked \
	apt-get update \
	&& apt-get install -y --no-install-recommends \
		network-manager \
		dbus \
		iproute2

RUN VERSION="33.5"; \
	TEMP=$(mktemp -d); \
    cd "${TEMP}" \
    && curl -o ./a.zip -fsSL "https://pkg.opt.td/protocolbuffers/protobuf/protoc@${VERSION}/linux/$(uname -m)" \
    && unzip ./a.zip -d /usr/local \
    && rm -rf "${TEMP}"
	
RUN VERSION="1.66"; \
	TEMP=$(mktemp -d); \
	cd "${TEMP}" \
	&& curl -o ./a -fsSL "https://pkg.opt.td/bufbuild/buf/buf@${VERSION}/linux/$(uname -m)" \
	&& chmod +x ./a \
	&& mv ./a /usr/local/bin/buf \
	&& rm -rf "${TEMP}"
