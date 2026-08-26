import asyncio
import os

import httpx

from nvoken import Behavior, Client


async def fetch(arguments, context):
    async with httpx.AsyncClient() as http:
        response = await http.get(arguments["url"], follow_redirects=True)
        response.raise_for_status()
        return response.text[:20_000]


async def main() -> None:
    behavior = Behavior(
        instructions="Use fetch to read the supplied public URL, then summarize it.",
        model=os.environ["NVOKEN_MODEL"],
        tools=({
            "mode": "host",
            "name": "fetch",
            "description": "Fetch one public URL",
            "input_schema": {
                "type": "object",
                "properties": {"url": {"type": "string"}},
                "required": ["url"],
                "additionalProperties": False,
            },
        },),
    )
    async with Client(os.environ["NVOKEN_API_KEY"]) as client:
        agent = client.inline(behavior).bind_tools({"fetch": fetch})
        print(await agent.text(
            f"Summarize {os.getenv('NVOKEN_FETCH_URL', 'https://example.com')}",
            tenant="example",
        ))


asyncio.run(main())
