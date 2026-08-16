import asyncio
import os

from nvoken import AgentDefinition, AgentOptions, Client, Model


async def main() -> None:
    async with Client(
        os.getenv("NVOKEN_BASE_URL", "http://localhost:8080"),
        os.environ["NVOKEN_API_KEY"],
    ) as client:
        definition = await client.create_agent_definition(
            "support",
            "Support",
            AgentDefinition(
                instructions="Help the customer with billing questions.",
                model=Model(provider="anthropic", id="claude-sonnet-5"),
            ),
            idempotency_key="quickstart-support-definition",
        )
        agents = await client.list_agents(agent_key="support")
        instance = agents.items[0] if agents.items else await client.create_agent(
            agent_key="support",
            name="Support",
            agent_definition_id=definition.id,
        )
        agent = client.agent(AgentOptions(agent_id=instance.id))
        print(f"agent> {await agent.text('Why was I charged twice?')}")


asyncio.run(main())
