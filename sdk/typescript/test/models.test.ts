import { describe, it, expect } from "vitest";
import {
  formatMinorDecimal,
  parseAccessPointSpec,
  parseCost,
  parseModelPrice,
  toWireAccessPointSpec,
  toWireModelPrice,
} from "../src/index.js";

describe("Wire and model helpers", () => {
  it("round-trips ModelPrice spec with identical keys", () => {
    const rawSpec = {
      currency: "USD",
      id: "mock-gpt",
      meters: {
        input_tokens: { denominator: 100, numerator: 15 },
        output_tokens: { denominator: 100, numerator: 60 },
      },
      model_id: "mock-gpt",
      scale: 6,
    };

    const parsed = parseModelPrice(rawSpec);
    const wire = toWireModelPrice(parsed);

    expect(Object.keys(wire).sort()).toEqual(Object.keys(rawSpec).sort());
    expect(wire).toEqual(rawSpec);
  });

  it("serializes AccessPointSpec to snake_case wire format", () => {
    const rawAp = {
      id: "local",
      display_name: "Local development",
      caller_kinds: ["user", "agent"],
      subjects: { principals: ["p1"] },
      models: [
        { name: "mock-gpt", surface_ref: "local-models", native_model: "mock-gpt" },
      ],
      tools: [{ connection_ref: "local-tools", expose: ["echo"] }],
      skills: [{ instance_ref: "skill-1" }],
      qos: { tier: "standard" },
    };

    const parsed = parseAccessPointSpec(rawAp);
    const wire = toWireAccessPointSpec(parsed);

    expect(wire).toEqual(rawAp);
  });

  it("computes exact decimal strings from integer minor units without floats", () => {
    expect(formatMinorDecimal("6", 6)).toBe("0.000006");
    expect(formatMinorDecimal("-1500", 2)).toBe("-15.00");
    expect(formatMinorDecimal("0", 4)).toBe("0.0000");
    expect(formatMinorDecimal("42", 0)).toBe("42");

    const cost = parseCost({
      basis: "settled",
      currency: "USD",
      amount: "12345678901234567890",
      scale: 6,
    });
    expect(cost.amountDecimal).toBe("12345678901234.567890");
  });
});
