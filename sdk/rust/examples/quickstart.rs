use nvoken::{Client, ExecutionSpec, InvokeRequest, Model};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let client = Client::new(
        std::env::var("NVOKEN_BASE_URL")?,
        std::env::var("NVOKEN_API_KEY")?,
    )?;
    let mut handle = client
        .invoke(InvokeRequest {
            agent_ref: "support".to_owned(),
            tenant_ref: None,
            session_id: None,
            session_key: None,
            idempotency_key: "ticket-42:message-1".to_owned(),
            input: "Why was I charged twice?".to_owned(),
            spec: ExecutionSpec {
                instructions: "Help the customer with billing questions.".to_owned(),
                model: Model {
                    provider: "anthropic".to_owned(),
                    name: "claude-sonnet-5".to_owned(),
                },
                budgets: None,
                tools: Vec::new(),
                output_schema: None,
            },
        })
        .await?;
    let invocation = handle.wait(None).await?;
    let result = handle.result().await?;
    println!("{} {:?}", invocation.id, invocation.status);
    if let Some(text) = result.output_text {
        println!("agent> {text}");
    }
    Ok(())
}
