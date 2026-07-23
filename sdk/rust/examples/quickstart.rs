use nvoken::{Client, InvokeRequest, Model};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let client = Client::new(
        std::env::var("NVOKEN_BASE_URL")?,
        std::env::var("NVOKEN_API_KEY")?,
    )?;
    let mut handle = client
        .invoke(InvokeRequest::new(
            "support",
            "Why was I charged twice?",
            Model {
                provider: "anthropic".to_owned(),
                id: "claude-sonnet-5".to_owned(),
            },
        ))
        .await?;
    let invocation = handle.wait(None).await?;
    let result = handle.result().await?;
    println!("{} {:?}", invocation.id, invocation.status);
    if let Some(text) = result.output_text {
        println!("agent> {text}");
    }
    Ok(())
}
