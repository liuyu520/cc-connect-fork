import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';
import { cn } from '@/lib/utils';

// Markdown 渲染组件，用于 Vibe Coding 聊天消息中的富文本展示
export default function VibeMarkdown({ content }: { content: string }) {
  return (
    <div
      className={cn(
        'prose max-w-none dark:prose-invert prose-sm',
        'prose-headings:font-semibold prose-headings:tracking-tight',
        'prose-p:my-2 prose-p:leading-relaxed',
        'prose-li:my-0.5',
        'prose-ul:my-2 prose-ol:my-2',
        'prose-a:text-accent prose-a:no-underline hover:prose-a:underline',
        'prose-code:text-pink-600 dark:prose-code:text-pink-400 prose-code:bg-gray-100 dark:prose-code:bg-gray-800 prose-code:px-1.5 prose-code:py-0.5 prose-code:rounded prose-code:text-xs',
        'prose-pre:bg-[#fafafa] dark:prose-pre:bg-[#0d1117] prose-pre:border prose-pre:border-gray-200 dark:prose-pre:border-gray-700/60 prose-pre:text-xs prose-pre:leading-relaxed'
      )}
    >
      <Markdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeHighlight]}>
        {content}
      </Markdown>
    </div>
  );
}
