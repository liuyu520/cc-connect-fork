import { useTranslation } from 'react-i18next';
import { Plus, X, Circle } from 'lucide-react';
import { cn } from '@/lib/utils';
import type { TabState } from './types';

const MAX_TABS = 10;

interface TabBarProps {
  tabs: TabState[];
  activeTabId: string;
  onSelectTab: (tabId: string) => void;
  onCloseTab: (tabId: string) => void;
  onNewTab: () => void;
}

// Tab 栏组件：展示所有标签页，支持切换、关闭和新建
export default function TabBar({ tabs, activeTabId, onSelectTab, onCloseTab, onNewTab }: TabBarProps) {
  const { t } = useTranslation();

  return (
    <div className="flex items-center gap-1 overflow-x-auto pb-2 border-b border-gray-200 dark:border-gray-800 scrollbar-thin">
      {tabs.map((tab) => {
        const isActive = tab.id === activeTabId;
        // Tab 显示名称：优先用 label（项目名），否则用 "New Tab"
        const displayName = tab.label || t('vibe.newTab');

        return (
          <div
            key={tab.id}
            onClick={() => onSelectTab(tab.id)}
            className={cn(
              'group flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium cursor-pointer transition-colors shrink-0 max-w-[180px]',
              isActive
                ? 'bg-accent/10 text-accent border border-accent/30'
                : 'bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-400 border border-transparent hover:bg-gray-200 dark:hover:bg-gray-700'
            )}
          >
            {/* 连接状态指示器 */}
            <Circle
              size={6}
              className={cn(
                'shrink-0 fill-current',
                tab.processAlive
                  ? 'text-emerald-500'
                  : tab.connectionStatus === 'connecting'
                    ? 'text-amber-500'
                    : 'text-gray-400 dark:text-gray-600'
              )}
            />
            {/* Tab 名称 */}
            <span className="truncate">{displayName}</span>
            {/* 关闭按钮：最后一个 Tab 不显示 */}
            {tabs.length > 1 && (
              <button
                onClick={(e) => {
                  e.stopPropagation();
                  onCloseTab(tab.id);
                }}
                className="ml-0.5 p-0.5 rounded opacity-0 group-hover:opacity-100 hover:bg-gray-300 dark:hover:bg-gray-600 transition-opacity"
              >
                <X size={10} />
              </button>
            )}
          </div>
        );
      })}

      {/* 新建 Tab 按钮 */}
      <button
        onClick={onNewTab}
        disabled={tabs.length >= MAX_TABS}
        title={tabs.length >= MAX_TABS ? t('vibe.maxTabsReached') : undefined}
        className="flex items-center gap-1 px-2 py-1.5 rounded-lg text-xs text-gray-500 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors shrink-0 disabled:opacity-30 disabled:cursor-not-allowed"
      >
        <Plus size={14} />
      </button>
    </div>
  );
}
