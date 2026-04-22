import { Daytona, Sandbox } from '@daytona/sdk'

async function main() {
  const daytona = new Daytona()

  let cursor: string | undefined
  do {
    const result = await daytona.list({
      cursor,
      limit: 10,
      labels: { env: 'dev' },
      states: ['started'],
      sort: 'createdAt',
      order: 'desc',
    })
    for (const sandbox of result.items) {
      console.log(sandbox.id)
    }
    cursor = result.nextCursor ?? undefined
  } while (cursor)
}

main().catch(console.error)
