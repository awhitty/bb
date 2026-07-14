# SOTA for Local-Model NL→Query over an Issue Tracker (mid-2026, research pass 2)

Research snapshot from 2026-07-08. First pass (licensing + distillation): `nlq-spike.md`.

Scope: what current literature says about (1) NL→structured-query compilation with small local models, (2) the agentic/direct-answer alternatives, (3) a ranked upgrade path for our `bd` TUI compiler (M5 Max, MLX/omlx, Qwen3.5-4B / Qwen3.6-35B-A3B / Qwen3-1.7B, ~100→low-thousands issues). Confidence tags: **HIGH** = multiple corroborating sources or primary paper; **MED** = single credible source; **LOW** = inference from adjacent evidence.

---

## 1. NL→structured query (text-to-SQL literature)

### 1.1 Execution-guided / self-repair loops

**How much it lifts.** Execution-feedback repair reliably adds mid-single-digit to double-digit points, with the gain concentrated in the first attempt:

- A 2026 study of iterative self-repair across model scales ([arXiv 2604.10508](https://arxiv.org/html/2604.10508)) — the most directly relevant scaling result: self-repair universally improves pass rates (+4.9 to +17.1 pp HumanEval, +16 to +30 pp MBPP); the smallest model tested (Llama 3.1 8B) gained +9.8 pp on HumanEval and +16 pp on MBPP; **two repair rounds capture 76–95% of achievable gains**; small models have the lowest per-attempt repair success (29.6% at 8B), so front-load quality into round 1. Modern instruction-tuned small models self-repair **without fine-tuning**, contradicting earlier findings. **HIGH**
- The Debugging Decay Index ([arXiv 2506.18403](https://arxiv.org/pdf/2506.18403), [Nature Sci. Reports version](https://www.nature.com/articles/s41598-025-27846-5)): effectiveness decays exponentially; essentially exhausted by attempt 3; interestingly Qwen-coder models retained repair capability slightly longer than GPT-class. **HIGH**
- A 2026 self-healing SQL pipeline reported up to **+9.3 pp** from the loop alone ([arXiv 2604.16511](https://arxiv.org/pdf/2604.16511)). **MED**
- **Critical caveat**: error-only feedback has a low ceiling in text-to-SQL — only ~3% of wrong SQL raises an execution error; the rest run "successfully" but wrong ([ErrorLLM, arXiv 2603.03742](https://arxiv.org/html/2603.03742)). The usable signals for the silent-failure majority are **empty results, suspicious row counts, and result samples**. Systems that inject "empty result ⇒ your filters are too strict / wrong value, relax or re-ground them" instructions on zero-row detection report this as a standard, effective trigger ([SDE-SQL](https://arxiv.org/pdf/2506.07245), [MCI-SQL](https://arxiv.org/pdf/2603.13390), [FISQL](https://openproceedings.org/2025/conf/edbt/paper-300.pdf)). **HIGH** (that the pattern is standard) / **MED** (magnitudes)

**Best-practice loop shape** (synthesis, **HIGH** on shape): max **2 revision attempts** (round 3+ is noise at small scale); feedback = (i) parse/execution error text verbatim, (ii) row count, (iii) 2–3 sample result rows, (iv) an explicit zero-row heuristic prompt naming the likely cause (bad enum value, wrong field, over-narrow AND) and offering the workspace's actual values.

**The closest published analog to our exact problem**: **Agentic Jackal — text-to-JQL** (Jira Query Language ≈ our tiny SQL-like DSL over an issue tracker), [arXiv 2604.09470](https://arxiv.org/abs/2604.09470). 100K validated NL–JQL pairs, execution-based eval. Findings: single-pass frontier LLMs averaged only **43.4%** execution accuracy on short queries; adding live execution via an MCP server improved 7 of 9 models (+9% relative on hard linguistic variants); **embedding-based grounding of categorical values ("JiraAnchor") was the biggest single lever: 48.7% → 71.7% on categorical-value queries, 16.9% → 66.2% on component fields**. Residual failures were semantic ambiguity, value resolution having been solved. This says the "title=reporting when it should have been an epic/label" failure we see is THE canonical failure mode of this task family, and value grounding beats loop mechanics as the fix. **HIGH** (abstract-verified numbers)

### 1.2 Schema/value grounding & few-shot selection

- **Similarity-selected few-shots** are established best practice: DAIL-SQL selects examples by masked-question + SQL-skeleton similarity and held the Spider top spot at 86.6% EX ([GitHub](https://github.com/BeachWang/DAIL-SQL)); OpenSearch-SQL's dynamic few-shot continues the line ([arXiv 2502.14913](https://arxiv.org/html/2502.14913v1)). **HIGH**
- **For small models specifically**, SPS-SQL pre-synthesizes query templates from the schema and retrieves them as few-shots: **81.7% Spider EX on Qwen2.5-Coder-7B** with no fine-tuning ([ScienceDirect](https://www.sciencedirect.com/science/article/abs/pii/S0167865525001497)). **MED**
- **Value injection**: current practice is entity extraction from the ask, then LSH + semantic-similarity lookup against actual database values, injecting matched candidates into the prompt ([survey of value retrieval practice](https://arxiv.org/pdf/2501.13594); [RASL, Amazon Science](https://assets.amazon.science/1b/95/8f62e89647348f4c4836f6c3040d/rasl-retrieval-augmented-schema-linking-for-massive-database-text-to-sql.pdf)). At our scale (~dozens of labels/epics/statuses) this degenerates to embedding ALL values and matching — trivial and high-yield per Jackal. **HIGH**

### 1.3 Constrained decoding beyond syntax

- Grammar-constrained decoding guarantees syntax only; semantic validity needs more. **IterGen** (ICLR 2025, [arXiv 2410.07295](https://arxiv.org/pdf/2410.07295)) does grammar-symbol-level backtracking with KV-cache reuse — e.g. regenerate just a bad WHERE literal. Language-server-assisted decoding enforces some semantic constraints (valid identifiers) during generation ([Correctness-Guaranteed Code Generation](https://arxiv.org/html/2508.15866v1)). **MED**
- Known hazard: hard constraints that force the model off its preferred tokens degrade semantic quality ([overview](https://mbrenndoerfer.com/writing/constrained-decoding-structured-llm-output)). **MED**
- Practical read for a tiny DSL: full semantic constrained decoding is over-engineering; a post-parse **validator** (fields exist, enum values exist, dates parse) that feeds violations into the repair loop achieves the same end with none of the decoding machinery. **LOW** (synthesis, consistent with the literature's cost/benefit)

### 1.4 Small-model SOTA on benchmarks, and the gap

| Model / system | BIRD EX | Note |
|---|---|---|
| SLM-SQL 0.5B | 56.9 dev / 61.8 test | SFT + RL + corrective self-consistency ([arXiv 2507.22478](https://arxiv.org/abs/2507.22478)) |
| SLM-SQL 1.5B | 67.1 dev / 70.5 test | same; avg +31.4 pp over base models |
| Arctic-Text2SQL-R1 7B | 68.5 | RL-trained; best 7B ([Snowflake](https://www.snowflake.com/en/blog/engineering/arctic-text2sql-r1-sql-generation-benchmark/)) |
| CSC-SQL 7B | 69.2 | self-consistency + self-correction combined |
| Arctic 32B | 71.8 | best open |
| CHASE-SQL (Gemini, 21 candidates) | 76.0 | multi-candidate + learned selector ([ICLR 2025](https://arxiv.org/html/2410.01943v1)) |
| Frontier pipelines 2026 | ~82 | AskData+GPT-4o test-set ([bird-bench.github.io](https://bird-bench.github.io/)) |

**HIGH** on the table. Reading: a **trained** 1.5B closes to within ~10 pp of frontier pipelines; the gap is closed by (i) execution-reward RL/SFT, (ii) multi-candidate generation + selection, (iii) grounding — techniques, none of which is raw scale. Untrained 4B-class models sit far lower single-shot. Spider 2.0 (enterprise, agentic; best systems ~31–51%: [ReFoRCE](https://haoailab.com/blogs/reforce/), [APEX-SQL](https://arxiv.org/abs/2602.16720)) is a difficulty class far above our tiny DSL — our task is Jackal-shaped, where grounding dominates. Also note both BIRD and Spider have documented annotation-error noise of several pp ([CIDR 2026](https://www.vldb.org/cidrdb/papers/2026/p5-jin.pdf)).

---

## 2. The more LLM-y framings

### 2.1 Agentic tool loop (small local models)

- The field has moved decisively agentic at the frontier: exploration-based agents (APEX-SQL: 70.65 BIRD / 51.0 Spider2-Snow, [arXiv 2602.16720](https://arxiv.org/abs/2602.16720); FlexSQL with on-demand DB exploration tools, [arXiv 2605.02815](https://arxiv.org/pdf/2605.02815)) beat single-shot pipelines. ReAct-style loops cut "wrong-direction" rates 6–30 pp vs single-shot on responsive models ([Live API-Bench](https://arxiv.org/pdf/2506.11266)). **HIGH**
- **But the multiplier is uneven**: the agentic-text-to-SQL literature itself notes exploration acts as "a performance multiplier, particularly for **stronger** models" ([EmergentMind survey](https://www.emergentmind.com/topics/agentic-text-to-sql-systems)); FlexSQL's open-model results start at **20B**; Jackal's agentic gains were on **frontier** models, and 2 of 9 models got worse. Meanwhile multi-turn interaction itself degrades small models: "LLMs Get Lost in Multi-Turn Conversation" measured a **112% increase in unreliability** multi-turn vs single-turn ([arXiv 2505.06120](https://arxiv.org/pdf/2505.06120)). Direct 4B-class tool-loop evals on data-query tasks are **scarce — evidence thin here**. The 35B-A3B is the credible loop runner on our hardware (reliable tool calling with 3B active is specifically reported: [LLMCheck](https://llmcheck.net/blog/qwen-36-35b-a3b-mac-new-number-one/)). **MED**
- Latency: each iteration is a full call; realistic loops are 3–8 iterations (mean 2.3–5.5 iterations reported for 7-8B ReAct). On-device that's roughly 10–40 s per ask on the 35B-A3B. **MED**

### 2.2 Whole-board-in-context QA

- Long-context degradation is confirmed as still real in 2026, worse for small models: performance drops with input length **even when the added tokens are irrelevant whitespace** (13.9–85% drops, Du et al. 2025 via [context-degradation survey](https://www.emergentmind.com/topics/context-degradation-in-llms)); lost-in-the-middle persists on 1M-token models ([DEV overview](https://dev.to/gabrielanhaia/lost-in-the-middle-is-still-real-in-2026-even-on-1m-token-models-2ehj)); models producing **structured/relational outputs** over in-context records stay under ~25% factual accuracy with degradation as output size grows ([arXiv 2505.21409](https://arxiv.org/pdf/2505.21409)). **HIGH** on direction, **MED** on magnitudes at exactly 4B.
- Verdict: fine as a fallback at ~100 issues for aggregate/fuzzy asks, and it matches our observed plateau; it does not scale to low-thousands of issues on a 4B, and enumeration-style asks ("all X") silently drop middle records. Consistent with what we already tried.

### 2.3 Embedding retrieval + rerank on small corpora

- Best local tiny stack as of mid-2026: **Qwen3-Embedding-0.6B** is the strongest sub-1GB embedder (64.33 multilingual MTEB, 32K context) ([Morph roundup](https://www.morphllm.com/ollama-embedding-models)); nomic-embed-text-v2 (137M) is the speed tier; bge-m3 gives dense+sparse in one model. Field has compressed — deployment constraints now dominate model choice ([BentoML guide](https://www.bentoml.com/blog/a-guide-to-open-source-embedding-models), [Apple Silicon comparison](https://contracollective.com/blog/local-embeddings-apple-silicon-nomic-bge-qwen3-m5-max-2026)). **HIGH**
- Standard architecture: BM25 + dense as parallel first-stage, RRF fusion, cross-encoder rerank of top-k ([Qdrant](https://qdrant.tech/documentation/tutorials-search-engineering/reranking-hybrid-search/), [2026 reference](https://www.digitalapplied.com/blog/hybrid-search-bm25-vector-reranking-reference-2026)). **Qwen3-Reranker-0.6B**: ~85–380 ms/query, near-8B quality when tuned ([HF](https://huggingface.co/Qwen/Qwen3-Reranker-0.6B), [Milvus writeup](https://milvus.io/blog/hands-on-rag-with-qwen3-embedding-and-reranking-models-using-milvus.md)). **HIGH**
- On ~100–1000 issues: embedding the whole corpus takes seconds; query-time cost is milliseconds. No published head-to-head of "embedding search vs LLM query compilation for fuzzy topical recall on tiny corpora" exists — **evidence gap** — but the Jackal result (topical/categorical recall is exactly where compilation fails, and embedding-based grounding is what fixed it) plus the complementary-failure-modes argument for hybrid retrieval make this a strong inference: for "find things about X", semantic retrieval beats field-match compilation. **LOW-MED** (inference).

---

## 3. Practical verdict — ranked upgrade paths

Latency baselines on M5 Max/MLX (**MED**, secondary benchmark sites): Qwen3.5-4B ≈ **148 tok/s** decode, TTFT 0.1–0.9 s; Qwen3.6-35B-A3B ≈ **55 tok/s**; M5 prefill ~4× M4 ([LLMCheck benchmarks](https://llmcheck.net/benchmarks), [M5 Max guide](https://aiproductivity.ai/blog/apple-m5-max-local-llm-guide/)). A compile (~60–120 output tokens) on the 4B ≈ ~1 s wall.

| Rank | Upgrade path | Expected lift | Effort | Latency cost | Evidence |
|---|---|---|---|---|---|
| 1 | **(a′) Execution-feedback repair loop, zero-row-aware** — max 2 retries; feed error text + row count + 2–3 sample rows; on 0 rows, explicitly instruct "wrong value or over-narrow filter — here are the workspace's actual labels/epics/statuses, broaden or re-map" | +5–15 pp on failing asks; most of the "0 results" class | Trivial (we already execute) | +1–2 s, only on asks that trigger it | HIGH |
| 2 | **Value/vocabulary grounding via embeddings** — embed all labels, epic titles, statuses, assignees; resolve the ask's entities to real values pre-compile; inject candidates into the prompt (lexical fuzzy matching is the no-download v1 at our tiny value-space scale) | Largest single lever in the closest published analog (Jackal: categorical 48.7→71.7) — directly attacks the `title=reporting` failure | Low-moderate | +10–50 ms | HIGH (Jackal), by strong analogy |
| 3 | **(b) Multi-strategy compile** — 2–3 candidates (narrow field-match, multi-field OR, parent/epic expansion), run all, merge, rank by cross-candidate vote (execution-based self-consistency) | The most robust known small-model lift (SLM-SQL corrective self-consistency; CSC-SQL 7B 69.2 BIRD); union directly fixes narrow recall | Moderate | 2–3× compile ≈ 2–4 s on the 4B | HIGH |
| 4 | **(e) Embedding tier as a parallel strategy** — hybrid BM25+dense (+0.6B rerank later) over title+description; route or merge for "about X" asks | Covers the fuzzy-recall class compilation can never win | Moderate | index seconds at startup; query <150 ms | MED |
| 5 | **(c) Retrieval-selected few-shots** — DAIL-style from our accepted-feedback JSONL | Meaningful but smaller at our DSL's simplicity | Low once the example bank exists | negligible prefill | HIGH (technique), MED (magnitude) |
| 6 | **(d) Tool-loop mini-agent on the 35B-A3B** | Highest ceiling, gains concentrate in stronger models; multi-turn drift at 4B; thin evidence at this scale | High | 10–40 s per ask | MED |
| 7 | **(f) Fine-tune (SFT → DPO on our feedback JSONL)** — accepted-vs-edited pairs are textbook DPO | Big (SLM-SQL: avg +31 pp at 0.5–1.5B; ExCoT execution-verified DPO from ~8K pairs) | Highest; wait for a few hundred–thousand pairs; MLX LoRA on-machine | none at inference | HIGH (technique), MED (our volume) |

**Recommended sequence**: 1 + 2 together (shared zero-row → re-ground pathway; one coherent change), then 3, then 4. Defer 6 until 1–4 plateau; accumulate the feedback JSONL toward 7 (it feeds 5 for free). One-line summary for this task family: **grounding failures, not syntax failures, cap small-model NL→query; execution feedback plus value grounding are the two cheapest fixes with the strongest evidence.**

(Sources inline above.)
