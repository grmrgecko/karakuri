// An MJPEG stream is a multipart/x-mixed-replace body: each part is a header
// block followed by one JPEG. Home Assistant declares its boundary with the
// leading dashes already on ("boundary=--frameboundary") and writes it
// unchanged, while a camera proxied straight through writes the standard
// "--" + boundary. Matching on "--" + the boundary without its own leading
// dashes finds both.

const encoder = new TextEncoder();
const decoder = new TextDecoder();
const HEADER_END = encoder.encode("\r\n\r\n");

// MAX_PART bounds the bytes buffered while looking for the end of one part,
// so a stream that is not MJPEG cannot grow the buffer without limit.
const MAX_PART = 16 * 1024 * 1024;

/** boundaryOf reads the boundary parameter of a multipart Content-Type. */
export function boundaryOf(contentType: string | null): string {
  const match = /boundary=(?:"([^"]+)"|([^;\s]+))/i.exec(contentType || "");
  return match ? match[1] || match[2] : "";
}

function indexOf(haystack: Uint8Array, needle: Uint8Array, from: number): number {
  const first = needle[0];
  const last = haystack.length - needle.length;
  outer: for (let i = Math.max(0, from); i <= last; i++) {
    if (haystack[i] !== first) continue;
    for (let j = 1; j < needle.length; j++) {
      if (haystack[i + j] !== needle[j]) continue outer;
    }
    return i;
  }
  return -1;
}

// trimTail drops the CRLF and stray dashes left before a delimiter. A JPEG
// ends with 0xFFD9, so none of these bytes can be part of the image.
function trimTail(part: Uint8Array): Uint8Array {
  let end = part.length;
  while (end > 0 && (part[end - 1] === 0x0d || part[end - 1] === 0x0a || part[end - 1] === 0x2d)) end--;
  return part.subarray(0, end);
}

/**
 * readMJPEG reads parts from the stream until it ends, handing each image to
 * onFrame. A part with a Content-Length header is cut at that length; one
 * without runs to the next delimiter. Cancelling the reader ends it.
 */
export async function readMJPEG(
  reader: ReadableStreamDefaultReader<Uint8Array>,
  boundary: string,
  onFrame: (frame: Blob) => void,
): Promise<void> {
  const delimiter = encoder.encode("--" + boundary.replace(/^--/, ""));
  let buffer = new Uint8Array(0);
  let inBody = false;
  let length = -1;
  let type = "image/jpeg";
  // scanFrom skips bytes already searched for a delimiter on earlier reads.
  let scanFrom = 0;

  for (;;) {
    const { value, done } = await reader.read();
    if (done) return;
    if (!value || value.length === 0) continue;

    const grown = new Uint8Array(buffer.length + value.length);
    grown.set(buffer);
    grown.set(value, buffer.length);
    buffer = grown;

    for (;;) {
      if (!inBody) {
        const end = indexOf(buffer, HEADER_END, 0);
        if (end < 0) {
          if (buffer.length > 64 * 1024) throw new Error("the camera stream is not MJPEG");
          break;
        }
        length = -1;
        type = "image/jpeg";
        for (const line of decoder.decode(buffer.subarray(0, end)).split(/\r?\n/)) {
          const match = /^([^:]+):\s*(.*)$/.exec(line);
          if (!match) continue;
          const name = match[1].trim().toLowerCase();
          if (name === "content-length") {
            const n = parseInt(match[2], 10);
            if (Number.isFinite(n) && n >= 0) length = n;
          } else if (name === "content-type" && match[2].trim()) {
            type = match[2].trim();
          }
        }
        buffer = buffer.slice(end + HEADER_END.length);
        inBody = true;
        scanFrom = 0;
      }

      let frame: Uint8Array;
      if (length >= 0) {
        if (buffer.length < length) break;
        frame = buffer.slice(0, length);
        buffer = buffer.slice(length);
      } else {
        const at = indexOf(buffer, delimiter, scanFrom);
        if (at < 0) {
          if (buffer.length > MAX_PART) throw new Error("a camera frame exceeded 16 MiB");
          scanFrom = Math.max(0, buffer.length - delimiter.length);
          break;
        }
        frame = trimTail(buffer.subarray(0, at)).slice();
        buffer = buffer.slice(at);
      }
      inBody = false;
      if (frame.length > 0) onFrame(new Blob([frame as BlobPart], { type }));
    }
  }
}
