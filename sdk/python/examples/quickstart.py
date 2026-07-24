import asyncio
import os

from nvoken import AgentOptions, Client, ExecutionSpec, Model


async def main() -> None:
    async with Client(
        os.getenv("NVOKEN_BASE_URL", "http://localhost:8080"),
        os.environ["NVOKEN_API_KEY"],
    ) as client:
        agent = client.agent(AgentOptions(
            agent_key="support",
            spec=ExecutionSpec(
                instructions="Help the customer with billing questions.",
                model=Model(provider="anthropic", id="claude-sonnet-5"),
            ),
        ))
        print(f"agent> {await agent.text('Why was I charged twice?')}")


asyncio.run(main())
