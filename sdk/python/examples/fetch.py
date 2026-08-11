import asyncio
import os

from nvoken import AgentOptions, Client, Model, fetch_tool


async def main() -> None:
    url = os.getenv("NVOKEN_FETCH_URL", "https://example.com")
    async with Client(
        os.getenv("NVOKEN_BASE_URL", "http://localhost:8080"),
        os.environ["NVOKEN_API_KEY"],
    ) as client:
        agent = client.agent(AgentOptions(
            agent_key="public-web-summary",
            instructions=(
                "Use nvoken_fetch to read the supplied public URL, then "
                "summarize only what the page says."
            ),
            model=Model(
                provider=os.environ["NVOKEN_PROVIDER"],
                id=os.environ["NVOKEN_MODEL"],
            ),
            tools=(fetch_tool(),),
        ))
        print(f"agent> {await agent.text(f'Summarize this public URL: {url}')}")


asyncio.run(main())
