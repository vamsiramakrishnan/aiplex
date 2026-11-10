import OpenAI from "openai";

// OPENAI_BASE_URL and OPENAI_API_KEY come from: eval "$(aiplex dev env)"
const client = new OpenAI();
const reply = await client.chat.completions.create({
  model: "mock-gpt",
  messages: [{ role: "user", content: "Hello" }],
});
console.log(reply.choices[0].message.content);
