from daytona import Daytona, ListSandboxesQuery


def main():
    daytona = Daytona()

    cursor = None
    while True:
        result = daytona.list(
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
    main()
