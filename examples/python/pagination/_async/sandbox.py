import asyncio

from daytona import AsyncDaytona, ListSandboxesQuery


async def main():
    async with AsyncDaytona() as daytona:
        cursor = None
        while True:
            result = await daytona.list(
                ListSandboxesQuery(
                    cursor=cursor,
                    limit=10,
                    labels={"env": "dev"},
                    states=["started"],
                    sort="createdAt",
                    order="desc",
                )
            )
            for sandbox in result.items:
                print(sandbox.id)
            cursor = result.next_cursor
            if not cursor:
                break


if __name__ == "__main__":
    asyncio.run(main())
