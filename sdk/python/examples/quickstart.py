import asyncio
import os

from nvoken import Client


async def main() -> None:
    async with Client(
        os.environ["NVOKEN_API_KEY"],
        base_url=os.getenv("NVOKEN_BASE_URL", "https://api.nvoken.com"),
    ) as client:
        analyst = await client.agent("real-estate-analyst")
        answer = await analyst.text(
            "Compare these two listings",
            tenant="acme",
            user="alice",
        )
        print(f"agent> {answer}")


asyncio.run(main())
