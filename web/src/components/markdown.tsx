import Markdown from 'react-markdown'

export function MarkdownView({ text }: { text: string }) {
  return (
    <div className="desk-md">
      <Markdown>{text}</Markdown>
    </div>
  )
}
