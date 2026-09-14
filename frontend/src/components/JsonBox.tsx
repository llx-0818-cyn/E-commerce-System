type JsonBoxProps = {
  value: unknown
}

export default function JsonBox({ value }: JsonBoxProps) {
  let text: string
  try {
    text = JSON.stringify(value, null, 2)
  } catch {
    text = String(value)
  }
  return <pre className="pre mono">{text}</pre>
}
