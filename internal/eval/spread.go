package eval

type Spread struct {
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	Mean float64 `json:"mean"`
	N    int     `json:"n"`
}

func (s Spread) add(v float64) Spread {
	if s.N == 0 || v < s.Min {
		s.Min = v
	}
	if s.N == 0 || v > s.Max {
		s.Max = v
	}
	s.Mean = (s.Mean*float64(s.N) + v) / float64(s.N+1)
	s.N++
	return s
}

func metricValues(m Metrics) map[string]*float64 {
	return map[string]*float64{
		"recall": m.Recall, "precision": m.Precision,
		"severity_accuracy": m.SeverityAccuracy, "false_positives_per_clean_case": m.FalsePositivesPerClean,
	}
}

func spreads(s Suite) map[string]Spread {
	values := map[string]Spread{}
	for repeat := 1; repeat <= s.Repeats; repeat++ {
		for _, partition := range []string{"overall", "development", "holdout"} {
			var selected []Result
			for _, result := range s.Cases {
				if result.Repeat == repeat && (partition == "overall" || result.Holdout == (partition == "holdout")) {
					selected = append(selected, result)
				}
			}
			addScoreSpread(values, partition, Aggregate(selected))
		}
	}
	// These spreads measure individual reviews, not replay wall-clock speed.
	for _, result := range s.Cases {
		prefix := "case/" + result.Case
		addScoreSpread(values, prefix, result.Scores)
		if result.LatencyMS != nil {
			values["latency_ms"] = values["latency_ms"].add(float64(*result.LatencyMS))
			values[prefix+"/latency_ms"] = values[prefix+"/latency_ms"].add(float64(*result.LatencyMS))
		}
		if result.Cost != nil {
			key := "cost_per_review/" + result.Cost.Currency
			values[key] = values[key].add(result.Cost.Amount)
			values[prefix+"/"+key] = values[prefix+"/"+key].add(result.Cost.Amount)
		}
		if result.FactoryCredits != nil {
			key := "factory_credits_per_review"
			values[key] = values[key].add(float64(*result.FactoryCredits))
			values[prefix+"/"+key] = values[prefix+"/"+key].add(float64(*result.FactoryCredits))
		}
	}
	return values
}

func addScoreSpread(values map[string]Spread, prefix string, scores Scores) {
	if scores.IncompleteRate != nil {
		key := prefix + "/incomplete_rate"
		values[key] = values[key].add(*scores.IncompleteRate)
	}
	for phase, metrics := range map[string]map[string]Metrics{"before": scores.Before, "after": scores.After} {
		for scope, m := range metrics {
			for name, v := range metricValues(m) {
				if v != nil {
					key := prefix + "/" + phase + "/" + scope + "/" + name
					values[key] = values[key].add(*v)
				}
			}
		}
	}
	for scope, v := range map[string]Verifier{
		"overall": scores.Verifier, "correctness": scores.VerifierByLayer["correctness"], "security": scores.VerifierByLayer["security"],
	} {
		key := prefix + "/verifier/" + scope
		for name, count := range map[string]int{
			"rejected_correctly": v.RejectedCorrectly, "rejected_real_bugs": v.RejectedRealBugs,
		} {
			values[key+"/"+name] = values[key+"/"+name].add(float64(count))
		}
		if v.RejectionRate != nil {
			values[key+"/rejection_rate"] = values[key+"/rejection_rate"].add(*v.RejectionRate)
		}
	}
}
