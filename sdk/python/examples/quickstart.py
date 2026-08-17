import asyncio
import os

from nvoken import AgentDefinition, AgentOptions, Client, Model


async def main() -> None:
    async with Client(
        os.getenv("NVOKEN_BASE_URL", "http://localhost:8080"),
        os.environ["NVOKEN_API_KEY"],
    ) as client:
        await client.create_agent_definition(AgentDefinition(
            definition_key="support",
            name="Support",
            instructions="Help the customer with billing questions.",
            model=Model(provider="anthropic", id="claude-sonnet-5"),
        ))
        # Declared from its keys. The Agent creates its record on first use.
        agent = client.agent(AgentOptions(agent_key="support", definition_key="support"))
        print(f"agent> {await agent.text('Why was I charged twice?')}")


asyncio.run(main())
