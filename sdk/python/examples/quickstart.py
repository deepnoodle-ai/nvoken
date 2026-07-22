import asyncio
import os

from nvoken import Client, ExecutionSpec, InvokeRequest, Model


async def main() -> None:
    async with Client(
        os.getenv("NVOKEN_BASE_URL", "http://localhost:8080"),
        os.environ["NVOKEN_API_KEY"],
    ) as client:
        handle = await client.invoke(InvokeRequest(
            agent_ref="support",
            idempotency_key="ticket-42:message-1",
            input="Why was I charged twice?",
            spec=ExecutionSpec(
                instructions="Help the customer with billing questions.",
                model=Model(provider="anthropic", name="claude-sonnet-5"),
            ),
        ))
        invocation = await handle.wait()
        result = await handle.result()
        print(invocation.id, invocation.status)
        if result.output_text is not None:
            print(f"agent> {result.output_text}")


asyncio.run(main())
