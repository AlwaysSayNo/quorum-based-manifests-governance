FROM golang:1.25

RUN set -eux; \
	apt-get update; \
	apt-get install -y --no-install-recommends \
		ca-certificates \
		curl \
		dirmngr \
		gettext \
		gnupg \
		libcurl4-openssl-dev \
		libexpat1-dev \
		libssl-dev \
		make \
		pkg-config \
		wget \
		xz-utils \
		build-essential; \
	rm -rf /var/lib/apt/lists/*


ARG KUBECTL_VERSION=1.35.0
ARG MINIKUBE_VERSION=1.37.0
ARG DOCKER_VERSION=29.1.3
ARG TASK_VERSION=3.46.3
ARG YQ_VERSION=4.52.2
ARG GIT_VERSION=2.47.3

ENV PATH=/usr/local/go/bin:/usr/local/bin:$PATH

RUN set -eux; \
	curl -fsSL "https://www.kernel.org/pub/software/scm/git/git-${GIT_VERSION}.tar.gz" -o /tmp/git.tgz; \
	tar -C /tmp -xzf /tmp/git.tgz; \
	cd "/tmp/git-${GIT_VERSION}"; \
	make prefix=/usr/local all; \
	make prefix=/usr/local install; \
	cd /; \
	rm -rf "/tmp/git-${GIT_VERSION}" /tmp/git.tgz; \
	git --version

RUN set -eux; \
	curl -fsSL "https://download.docker.com/linux/static/stable/x86_64/docker-${DOCKER_VERSION}.tgz" -o /tmp/docker.tgz; \
	tar -C /tmp -xzf /tmp/docker.tgz; \
	mv /tmp/docker/docker /usr/local/bin/; \
	rm -rf /tmp/docker /tmp/docker.tgz; \
	docker --version

RUN set -eux; \
	curl -fsSL "https://dl.k8s.io/release/v${KUBECTL_VERSION}/bin/linux/amd64/kubectl" -o /usr/local/bin/kubectl; \
	chmod +x /usr/local/bin/kubectl; \
	kubectl version --client=true

RUN set -eux; \
	curl -fsSL "https://storage.googleapis.com/minikube/releases/v${MINIKUBE_VERSION}/minikube-linux-amd64" -o /usr/local/bin/minikube; \
	chmod +x /usr/local/bin/minikube; \
	minikube version

RUN set -eux; \
	GOBIN=/usr/local/bin go install "github.com/go-task/task/v3/cmd/task@v${TASK_VERSION}"; \
	task --version

RUN set -eux; \
	GOBIN=/usr/local/bin go install "github.com/mikefarah/yq/v4@v${YQ_VERSION}"; \
	yq --version
