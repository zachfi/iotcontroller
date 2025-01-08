// local image = 'zachfi/shell:latest';
local registry = 'reg.dist.svc.cluster.znet:5000';

local pipeline(name) = {
  kind: 'pipeline',
  name: name,
  steps: [],
  depends_on: [],
  volumes: [
    // { name: 'cache', temp: {} },
    { name: 'dockersock', host: { path: '/var/run/docker.sock' } },
  ],
  trigger: {
    ref: [
      'refs/heads/main',
      'refs/heads/dependabot/**',
      'refs/pull/*/head',
    ],
  },
};

local withPipelineOnlyTags() = {
  trigger+: {
    ref: [
      'refs/tags/v*',
    ],
  },
};

local withStepDockerSock() = {
  volumes+: [
    { name: 'dockersock', path: '/var/run/docker.sock' },
  ],
};

local buildImage(image='zachfi/build-image') = withStepDockerSock() {
  name: 'build-image',
  image: image,
  when: {
    ref: [
      'refs/heads/main',
    ],
  },
  commands: [
    'sudo make docker-build registry=%s' % registry,
  ],
};


local pushImage(image='zachfi/build-image') = withStepDockerSock() {
  name: 'push-image',
  image: image,
  when: {
    ref: [
      'refs/heads/main',
    ],
  },
  commands:
    [
      'sudo make docker-push registry=%s' % registry,
    ],
};

local test() = {
  name: 'test',
  image: 'golang',
  commands: [
    'make test',
  ],
};

local step(name, image='zachfi/build-image') = {
  name: name,
  image: image,
  pull: 'always',
  commands: [],
};

local make(target) = step(target) {
  commands: ['make %s' % target],
};

local withGithub() = {
  environment+: {
    GITHUB_TOKEN: {
      from_secret: 'GITHUB_TOKEN',
    },
  },
};

local withDockerHub() = {
  environment+: {
    DOCKER_PASSWORD: {
      from_secret: 'DOCKER_PASSWORD',
    },
    DOCKER_USERNAME: {
      from_secret: 'DOCKER_USERNAME',
    },
  },
};

local withTags() = {
  when+: {
    ref+: [
      'refs/tags/v*',
    ],
  },
};

[
  (
    pipeline('ci') {
      steps: [
        test(),
        make('test-e2e'),
        buildImage(),
        pushImage()
        + withDockerHub(),
      ],
    }
  ),
  (
    pipeline('release')
    + withPipelineOnlyTags() {
      steps:
        [
          make('release')
          + withGithub()
          + withTags(),
        ],
    }
  ),
]
