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
  ],
}
