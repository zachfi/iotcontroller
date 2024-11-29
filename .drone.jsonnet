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
    event: ['pull_request'],
    action: ['opened'],
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

[
  (
    pipeline('ci') {
      steps: [
        test(),
        buildImage(),
      ],
    }
  ),
]
