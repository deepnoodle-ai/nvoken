use nvoken::{Client, InvokeRequest, Model, WaitOptions};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let client = Client::new(
        std::env::var("NVOKEN_BASE_URL")?,
        std::env::var("NVOKEN_API_KEY")?,
    )?;
    let request = InvokeRequest::new(
        "support",
        "Why was I charged twice?",
        Model::new("anthropic", "claude-sonnet-5"),
    )
    .instructions("Help the customer with billing questions.");
    let mut handle = client.invoke(request).await?;
    let invocation = handle.wait_with_options(WaitOptions::default()).await?;
    let result = handle.result().await?;
    println!("{} {:?}", invocation.id, invocation.status);
    if let Some(text) = result.output_text {
        println!("agent> {text}");
    }
    Ok(())
}
