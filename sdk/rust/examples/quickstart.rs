use nvoken::{Client, TurnOptions};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let api_key = std::env::var("NVOKEN_API_KEY")?;
    let client = match std::env::var("NVOKEN_BASE_URL") {
        Ok(base_url) => Client::with_base_url(api_key, base_url),
        Err(_) => Client::new(api_key),
    };

    let analyst = client.agent("real-estate-analyst", None).await?;
    let answer = analyst
        .text(
            "Compare these two listings",
            TurnOptions::new("acme").user("alice"),
        )
        .await?;
    println!("{answer}");
    Ok(())
}
