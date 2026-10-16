import os
from typing import List, Union
from fastapi import FastAPI
from pydantic import BaseModel
from sentence_transformers import SentenceTransformer
import torch

MODEL_NAME = os.getenv("EMBED_MODEL", "nomic-ai/nomic-embed-text-v2-moe")

# Auto-detect optimal compute device (CUDA GPU, Apple MPS, or CPU)
if torch.cuda.is_available():
    DEVICE = "cuda"
elif torch.backends.mps.is_available():
    DEVICE = "mps"
else:
    DEVICE = "cpu"

DEVICE = os.getenv("DEVICE", DEVICE)

app = FastAPI(
    title="Engram Nomic Embed Sidecar",
    description="High-throughput Uvicorn/FastAPI sidecar serving nomic-ai/nomic-embed-text-v2-moe",
    version="0.1.0"
)

# Load model into memory/VRAM once at initialization
print(f"Loading {MODEL_NAME} on device: {DEVICE}...")
model = SentenceTransformer(MODEL_NAME, trust_remote_code=True, device=DEVICE)
print("Model loaded successfully and ready for inference.")

class EmbedRequest(BaseModel):
    input: Union[str, List[str]]
    model: str = MODEL_NAME

class EmbedResponseItem(BaseModel):
    index: int
    embedding: List[float]

class EmbedResponse(BaseModel):
    object: str = "list"
    data: List[EmbedResponseItem]
    model: str
    usage: dict

@app.get("/health")
def health():
    return {
        "status": "ok",
        "model": MODEL_NAME,
        "device": DEVICE,
        "dimensions": 768
    }

@app.post("/v1/embeddings", response_model=EmbedResponse)
@app.post("/embed", response_model=EmbedResponse)
def create_embeddings(req: EmbedRequest):
    texts = [req.input] if isinstance(req.input, str) else req.input
    if not texts:
        return EmbedResponse(data=[], model=req.model, usage={"total_tokens": 0})
    
    # Generate embeddings with L2 normalization (cosine similarity ready)
    embeddings = model.encode(texts, normalize_embeddings=True, show_progress_bar=False)
    
    data = [
        EmbedResponseItem(index=i, embedding=emb.tolist())
        for i, emb in enumerate(embeddings)
    ]
    
    return EmbedResponse(
        data=data,
        model=req.model,
        usage={"prompt_tokens": len(texts), "total_tokens": len(texts)}
    )

if __name__ == "__main__":
    import uvicorn
    port = int(os.getenv("PORT", "8001"))
    uvicorn.run("main:app", host="0.0.0.0", port=port, log_level="info")
