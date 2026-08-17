use nvoken::{AgentDefinition, AgentOptions, Client, Model};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let client = Client::new(
        std::env::var("NVOKEN_BASE_URL")?,
        std::env::var("NVOKEN_API_KEY")?,
    )?;
    client
        .create_agent_definition(
            "quickstart-support-definition",
            "support",
            "Support",
            AgentDefinition::new(Model::new("anthropic", "claude-sonnet-5"))
                .instructions("Help the customer with billing questions."),
        )
        .await?;
    // Declared from its keys. The Agent creates its record on first use, so
    // running this twice resolves onto the same one.
    let agent = client.agent(AgentOptions::declared("support", "support"))?;
    println!(
        "agent> {}",
        agent
            .text("Why was I charged twice?", Default::default())
            .await?
    );
    Ok(())
}
