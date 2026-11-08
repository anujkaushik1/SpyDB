import { createContext } from 'react'
import type { CompletionSchema } from './sqlCompletion'

// The open connection's tables and columns for SQL autocomplete (from
// completionSchema). App provides it; every SQL editor reads it.
export const SqlSchemaContext = createContext<CompletionSchema | undefined>(undefined)
