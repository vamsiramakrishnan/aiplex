from openai import OpenAI

# OPENAI_BASE_URL and OPENAI_API_KEY come from: eval "$(aiplex dev env)"
client = OpenAI()
reply = client.chat.completions.create(
    model="mock-gpt", messages=[{"role": "user", "content": "Hello"}]
)
print(reply.choices[0].message.content)
