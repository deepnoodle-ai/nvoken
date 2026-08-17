use nvoken::{AgentDefinition, AgentOptions, Client, CreateAgentDefinitionOptions, Model};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let client = Client::new(
        std::env::var("NVOKEN_BASE_URL")?,
        std::env::var("NVOKEN_API_KEY")?,
    )?;
    client
        .create_agent_definition(
            AgentDefinition::new(Model::new("anthropic", "claude-sonnet-5"))
                .definition_key("support")
                .name("Support")
                .instructions("Help the customer with billing questions."),
            CreateAgentDefinitionOptions::default(),
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
