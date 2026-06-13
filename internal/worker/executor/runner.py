import sys
import os
import socket
import struct
from sentence_transformers import SentenceTransformer
import google.protobuf
import worker_pb2

# Set Hugging Face Hub cache directory locally
os.environ["HF_HOME"] = os.path.join(os.path.dirname(__file__), "hf_cache")

def read_exact(sock, n):
    data = bytearray()
    while len(data) < n:
        packet = sock.recv(n - len(data))
        if not packet:
            return None
        data.extend(packet)
    return bytes(data)

def handle_client(sock, model):
    while True:
        # Read 4-byte big-endian message length
        len_bytes = read_exact(sock, 4)
        if not len_bytes:
            break
        
        msg_len = struct.unpack(">I", len_bytes)[0] # > Big Endian format and I means Unsigned 32 bit integer
        
        # Read the message payload
        req_bytes = read_exact(sock, msg_len)
        if not req_bytes:
            break
        
        req = worker_pb2.JobRequest()
        req.ParseFromString(req_bytes)
        
        # Run inference
        resp = worker_pb2.JobResponse()
        resp.job_id = req.job_id
        resp.worker_id = "local-runner"
        
        try:
            embeddings = model.encode(req.input_text).tolist()
            resp.success = True
            resp.embedding.extend(embeddings)
        except Exception as e:
            resp.success = False
            resp.error = str(e)
        
        # Serialize response
        resp_bytes = resp.SerializeToString()
        header = struct.pack(">I", len(resp_bytes))
        
        # Write response back to socket
        sock.sendall(header + resp_bytes)

if __name__ == "__main__":
    if len(sys.argv) < 3:
        print("Usage: python runner.py <model_name> <socket_path>", flush=True)
        sys.exit(1)

    model_name = sys.argv[1]
    socket_path = sys.argv[2]

    # Resolve generic model key to hugging face repository ID, falling back to direct repo path
    hf_model = model_name
    if model_name == "all-minilm":
        hf_model = "sentence-transformers/all-MiniLM-L6-v2"
    elif model_name == "clip":
        hf_model = "sentence-transformers/clip-ViT-B-32"

    print(f"Downloading and loading model '{hf_model}' from Hugging Face...", flush=True)
    model = SentenceTransformer(hf_model, trust_remote_code=True)
    print("Model loaded successfully!", flush=True)

    # Clean up any existing socket file
    if os.path.exists(socket_path):
        os.remove(socket_path)

    # Ensure parent directory of the socket path exists
    os.makedirs(os.path.dirname(os.path.abspath(socket_path)), exist_ok=True)

    server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) # AF_UNIX vs AF_INET : unix uses file system and AF_INET uses IP address + port
    # SOCK_STREAM - Biderictional reliable byte stream
    # SOCK_DGRAM - Unreliable
    server.bind(socket_path) # Binding the socket to the path
    server.listen(5) # 
    print(f"Runner UDS server listening on {socket_path}", flush=True)

    try:
        while True:
            client_sock, addr = server.accept()
            try:
                handle_client(client_sock, model)
            except Exception as e:
                print(f"Error handling client: {e}", flush=True)
            finally:
                client_sock.close()
    except KeyboardInterrupt:
        print("Shutting down runner server...", flush=True)
    finally:
        server.close()
        if os.path.exists(socket_path):
            os.remove(socket_path)
