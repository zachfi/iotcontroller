local image = 'zachfi/shell:latest';


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
    ],
  },
};

local withPipelineTags() = {
  trigger+: {
    ref+: [
      'refs/tags/v*',
    ],
  },
};

local buildImage() = {
  name: 'build-image',
  image: image,
  commands:
    [
      'sudo make docker-build',
    ],
  volumes+: [
    { name: 'dockersock', path: '/var/run/docker.sock' },
  ],
};

local test() = {
  name: 'test',
  image: 'golang',
  commands: [
    'make test',
  ],
};

local step(name) = {
  name: name,
  image: 'zachfi/build-image',
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
        buildImage(),
      ],
    }
  ),
  (
    pipeline('release')
    + withPipelineTags() {
      steps:
        [
          make('release')
          + withGithub()
          + withTags(),
        ],
    }
  ),
]
