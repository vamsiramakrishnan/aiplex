import copy
import json
from pathlib import Path
import tempfile
import unittest

import compare_billing as billing


class BillingEvidenceTests(unittest.TestCase):
    def setUp(self):
        self.record = {"tenant_id": "tenant", "attempt_id": "attempt", "model_version": "model-v1", "counters": {"uncached": 7, "output": 2}, "money": {"USD": {"scale": 6, "amount": 11}}, "evidence_ref": "provider-receipt"}

    def load(self, content):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "evidence.jsonl"
            path.write_text(content)
            return billing.load(path)

    def test_exact_comparison_and_no_missing_identity_substitution(self):
        evidence = self.load(json.dumps(self.record))
        self.assertTrue(billing.compare(evidence, evidence)["matched"])
        other = copy.deepcopy(self.record)
        other["tenant_id"] = "other"
        report = billing.compare(evidence, self.load(json.dumps(other)))
        self.assertFalse(report["matched"])
        self.assertEqual(report["missing_attempts"], 2)

    def test_model_counter_currency_scale_and_charge_are_independent(self):
        original = self.load(json.dumps(self.record))
        for field in ("model", "counter", "currency", "scale", "amount"):
            with self.subTest(field=field):
                changed = copy.deepcopy(self.record)
                if field == "model": changed["model_version"] = "other-version"
                elif field == "counter": changed["counters"]["uncached"] += 1
                elif field == "currency": changed["money"] = {"AUD": changed["money"]["USD"]}
                elif field == "scale": changed["money"]["USD"]["scale"] = 9
                else: changed["money"]["USD"]["amount"] += 1
                self.assertFalse(billing.compare(original, self.load(json.dumps(changed)))["matched"])
        changed = copy.deepcopy(self.record)
        changed["money"]["USD"]["amount"] += 1
        self.assertTrue(billing.compare(original, self.load(json.dumps(changed)), tolerance=1)["matched"])

    def test_rejects_ambiguous_lossy_and_duplicate_evidence(self):
        valid = json.dumps(self.record)
        invalid = [valid + "\n" + valid, valid.replace('"uncached": 7', '"uncached": 7.0'), valid.replace('"uncached": 7', '"uncached": 1e-400'), valid.replace('"uncached": 7', '"uncached": true'), valid.replace('"uncached": 7', '"uncached": -1'), valid.replace('"uncached": 7', '"uncached": 9223372036854775808'), valid.replace('"uncached": 7', '"uncached": 7, "uncached": 7'), ""]
        for content in invalid:
            with self.subTest(content=content):
                with self.assertRaises(ValueError): self.load(content)


if __name__ == "__main__":
    unittest.main()
