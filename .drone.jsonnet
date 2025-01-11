// local image = 'zachfi/shell:latest';
local registry = 'reg.dist.svc.cluster.znet:5000';
local defaultImage = '%s/zachfi/build-image' % registry;

local pipeline(name) = {
  kind: 'pipeline',
  name: name,
  steps: [],
  depends_on: [],
  volumes: [
    { name: 'dockersock', temp: {} },
    // { name: 'dockersock', host: { path: '/var/run/docker.sock' } },
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
    // { name: 'dockersock', path: '/var/run/docker.sock' },
    { name: 'dockersock', path: '/var/run' },
  ],
};

local buildImage(image=defaultImage) = withStepDockerSock() {
  name: 'build-image',
  image: image,
  when: {
    ref: [
      'refs/heads/main',
    ],
  },
  commands: [
    'make docker-build registry=%s' % registry,
  ],
};


local pushImage(image=defaultImage) = withStepDockerSock() {
  name: 'push-image',
  image: image,
  when: {
    ref: [
      'refs/heads/main',
    ],
  },
  commands:
    [
      'make docker-push registry=%s' % registry,
    ],
};

local test() = {
  name: 'test',
  image: 'golang',
  commands: [
    'make test',
  ],
};

local step(name, image=defaultImage) = {
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
        // make('test-e2e')
        // + withStepDockerSock(),
        buildImage(),
        pushImage()
        + withDockerHub(),
      ],
      services: [
        {
          name: 'docker',
          image: 'docker:dind',
          privileged: true,
        }
        + withStepDockerSock(),
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
