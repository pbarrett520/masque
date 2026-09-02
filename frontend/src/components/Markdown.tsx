import { memo } from "react";
import ReactMarkdown from "react-markdown";
import remarkBreaks from "remark-breaks";
import remarkGfm from "remark-gfm";

const plugins = [remarkGfm, remarkBreaks];

// Renders chat content as markdown. Raw HTML in the source is escaped, not
// rendered (react-markdown default), so model output can't inject markup.
// remark-breaks turns single newlines into <br> — roleplay prose relies on
// line breaks that standard markdown would otherwise fold into one paragraph.
export const Markdown = memo(function Markdown({ text }: { text: string }) {
  return (
    <div className="markdown">
      <ReactMarkdown remarkPlugins={plugins}>{text}</ReactMarkdown>
    </div>
  );
});
