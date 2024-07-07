local cacheBase = '/repo';
local image = 'zachfi/shell:latest';

local buildImage() = {
  name: 'build-image',
  image: image,
  commands:
    [
      'make docker-build',
    ],
  volumes+: [
    { name: 'dockersock', path: '/var/run/docker.sock' },
  ],
};

{
  local this = self,

  kind: 'pipeline',
  // type: 'kubernetes',
  name: 'ci',
  steps: [
           {
             name: 'test',
             image: 'golang',
             commands: [
               'make test',
             ],
           },
         ]
         + [
           buildImage(),
         ],
}
