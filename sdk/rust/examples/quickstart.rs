use nvoken::{AgentDefinition, AgentOptions, Client, CreateAgentInput, ListAgentsOptions, Model};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let client = Client::new(
        std::env::var("NVOKEN_BASE_URL")?,
        std::env::var("NVOKEN_API_KEY")?,
    )?;
    let definition = client
        .create_agent_definition(
            "quickstart-support-definition",
            "support",
            "Support",
            AgentDefinition::new(Model::new("anthropic", "claude-sonnet-5"))
                .instructions("Help the customer with billing questions."),
        )
        .await?;
    let agents = client
        .list_agents(ListAgentsOptions {
            agent_key: Some("support".to_owned()),
            ..Default::default()
        })
        .await?;
    let instance = if let Some(instance) = agents.items.into_iter().next() {
        instance
    } else {
        client
            .create_agent(CreateAgentInput {
                tenant_key: None,
                agent_key: "support".to_owned(),
                name: "Support".to_owned(),
                agent_definition_id: definition.id,
                pinned_revision: None,
            })
            .await?
    };
    let agent = client.agent(AgentOptions::from_agent_id(instance.id))?;
    println!(
        "agent> {}",
        agent
            .text("Why was I charged twice?", Default::default())
            .await?
    );
    Ok(())
}
