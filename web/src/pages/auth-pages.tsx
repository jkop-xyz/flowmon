import { useMutation, useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, CheckCircle2, KeyRound } from "lucide-react"
import { useState, type FormEvent, type ReactNode } from "react"

import { Field, FormActions, FormError, Logo } from "@/components/page"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { configureDatabase, createAdmin, login, testDatabase, type DatabaseChoice, type SetupStatus } from "@/lib/auth"

function Screen({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-svh flex-col items-center justify-center gap-6 bg-muted/40 p-4">
      <div className="flex items-center gap-2 text-lg font-semibold">
        <Logo />
        Flowmon
      </div>
      {children}
    </div>
  )
}

export function LoginPage() {
  const queryClient = useQueryClient()
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const mutation = useMutation({
    mutationFn: () => login(username, password),
    onSuccess: () => queryClient.invalidateQueries(),
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    mutation.mutate()
  }

  return (
    <Screen>
      <Card className="w-full max-w-sm">
        <form onSubmit={submit}>
          <CardHeader>
            <CardTitle>Log in</CardTitle>
            <CardDescription>Use the account created during setup.</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4 py-6">
            <Field id="username" label="Username">
              <Input
                id="username"
                autoComplete="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                required
                autoFocus
              />
            </Field>
            <Field id="password" label="Password">
              <Input
                id="password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </Field>
            <FormError error={mutation.error} />
            <Button type="submit" className="w-full" disabled={mutation.isPending}>
              {mutation.isPending ? "Logging in…" : "Log in"}
            </Button>
            <p className="text-xs text-muted-foreground">
              Forgot the password? Run <code className="font-mono">flowmon admin reset-password &lt;username&gt;</code>{" "}
              where Flowmon runs.
            </p>
          </CardContent>
        </form>
      </Card>
    </Screen>
  )
}

type PostgresFields = Extract<DatabaseChoice, { driver: "postgres" }>

const emptyPostgres: PostgresFields = {
  driver: "postgres",
  host: "",
  port: 5432,
  database: "flowmon",
  user: "flowmon",
  password: "",
  schema: "flowmon",
  sslMode: "prefer",
}

export function SetupPage({ status }: { status: SetupStatus }) {
  const [token, setToken] = useState("")
  return (
    <Screen>
      <Card className="w-full max-w-lg">
        <CardHeader>
          <CardTitle>Set up Flowmon</CardTitle>
          <CardDescription>
            Step {status.step === "database" ? "1" : "2"} of 2:{" "}
            {status.step === "database" ? "where Flowmon keeps accounts and settings" : "create the admin account"}.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-6">
          <Field
            id="token"
            label="Setup token"
            hint={
              <>
                Printed in Flowmon's logs when it starts, e.g. <code className="font-mono">docker logs flowmon</code>.
                It proves you run this server.
              </>
            }
          >
            <div className="relative">
              <KeyRound className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                id="token"
                className="pl-9 font-mono"
                placeholder="XXXXXX-XXXXXX-XXXXXX-XXXXXX"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                autoComplete="off"
                spellCheck={false}
              />
            </div>
          </Field>
          {status.step === "database" ? <DatabaseStep status={status} token={token} /> : <AdminStep token={token} />}
        </CardContent>
      </Card>
    </Screen>
  )
}

function DatabaseStep({ status, token }: { status: SetupStatus; token: string }) {
  const queryClient = useQueryClient()
  const [driver, setDriver] = useState<"sqlite" | "postgres">("sqlite")
  const [pg, setPg] = useState<PostgresFields>(emptyPostgres)
  const choice: DatabaseChoice = driver === "sqlite" ? { driver: "sqlite" } : pg

  const test = useMutation({ mutationFn: () => testDatabase(token, choice) })
  const save = useMutation({
    mutationFn: () => configureDatabase(token, choice),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["setup"] }),
  })
  const set = (field: keyof PostgresFields) => (e: React.ChangeEvent<HTMLInputElement>) => {
    test.reset()
    setPg({ ...pg, [field]: field === "port" ? Number(e.target.value) : e.target.value })
  }

  if (status.databaseLocked) {
    return (
      <Alert variant={status.databaseUnavailable ? "destructive" : "default"}>
        <AlertTriangle />
        <AlertTitle>The database is set by FLOWMON_DATABASE_URL</AlertTitle>
        <AlertDescription>
          {status.databaseUnavailable
            ? "Flowmon can't reach it and keeps retrying every 30 seconds. Check the URL and the database server, then reload this page."
            : "Reload this page once Flowmon has connected."}
        </AlertDescription>
      </Alert>
    )
  }

  return (
    <form
      className="flex flex-col gap-6"
      onSubmit={(e) => {
        e.preventDefault()
        save.mutate()
      }}
    >
      {status.databaseUnavailable && (
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertDescription>
            A database was configured before but can't be reached. Choosing again replaces that choice.
          </AlertDescription>
        </Alert>
      )}
      <RadioGroup
        value={driver}
        onValueChange={(v) => setDriver(v as "sqlite" | "postgres")}
        className="grid gap-3 sm:grid-cols-2"
      >
        <Label
          htmlFor="db-sqlite"
          className="flex cursor-pointer items-start gap-3 rounded-lg border p-3 has-[[data-state=checked]]:border-primary"
        >
          <RadioGroupItem id="db-sqlite" value="sqlite" className="mt-0.5" />
          <span className="flex flex-col gap-1">
            <span className="font-medium">Built-in (SQLite)</span>
            <span className="text-xs font-normal text-muted-foreground">
              Stored in the data volume. Nothing to set up.
            </span>
          </span>
        </Label>
        <Label
          htmlFor="db-postgres"
          className="flex cursor-pointer items-start gap-3 rounded-lg border p-3 has-[[data-state=checked]]:border-primary"
        >
          <RadioGroupItem id="db-postgres" value="postgres" className="mt-0.5" />
          <span className="flex flex-col gap-1">
            <span className="font-medium">PostgreSQL</span>
            <span className="text-xs font-normal text-muted-foreground">
              Your existing server, backed up with the rest.
            </span>
          </span>
        </Label>
      </RadioGroup>

      {driver === "postgres" && (
        <div className="grid gap-4 sm:grid-cols-6">
          <div className="sm:col-span-4">
            <Field id="pg-host" label="Host">
              <Input id="pg-host" value={pg.host} onChange={set("host")} placeholder="postgres.lan" required />
            </Field>
          </div>
          <div className="sm:col-span-2">
            <Field id="pg-port" label="Port">
              <Input id="pg-port" type="number" min={1} max={65535} value={pg.port} onChange={set("port")} required />
            </Field>
          </div>
          <div className="sm:col-span-3">
            <Field id="pg-database" label="Database">
              <Input id="pg-database" value={pg.database} onChange={set("database")} required />
            </Field>
          </div>
          <div className="sm:col-span-3">
            <Field id="pg-schema" label="Schema" hint="Created if it doesn't exist.">
              <Input id="pg-schema" value={pg.schema} onChange={set("schema")} />
            </Field>
          </div>
          <div className="sm:col-span-3">
            <Field id="pg-user" label="User">
              <Input id="pg-user" value={pg.user} onChange={set("user")} autoComplete="off" required />
            </Field>
          </div>
          <div className="sm:col-span-3">
            <Field id="pg-password" label="Password">
              <Input
                id="pg-password"
                type="password"
                value={pg.password}
                onChange={set("password")}
                autoComplete="new-password"
              />
            </Field>
          </div>
          <div className="sm:col-span-6">
            <Field id="pg-sslmode" label="SSL mode">
              <Select
                value={pg.sslMode}
                onValueChange={(v) => {
                  test.reset()
                  setPg({ ...pg, sslMode: v })
                }}
              >
                <SelectTrigger id="pg-sslmode" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {status.sslModes.map((m) => (
                    <SelectItem key={m} value={m}>
                      {m}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </div>
        </div>
      )}

      {test.isSuccess && (
        <Alert>
          <CheckCircle2 />
          <AlertDescription>Connected to PostgreSQL.</AlertDescription>
        </Alert>
      )}
      <FormError error={test.error ?? save.error} />

      <FormActions>
        {driver === "postgres" && (
          <Button type="button" variant="outline" disabled={!token || test.isPending} onClick={() => test.mutate()}>
            {test.isPending ? "Testing…" : "Test connection"}
          </Button>
        )}
        <Button type="submit" disabled={!token || save.isPending}>
          {save.isPending ? "Setting up…" : "Continue"}
        </Button>
      </FormActions>
    </form>
  )
}

function AdminStep({ token }: { token: string }) {
  const queryClient = useQueryClient()
  const [username, setUsername] = useState("admin")
  const [password, setPassword] = useState("")
  const [repeat, setRepeat] = useState("")
  const mismatch = repeat !== "" && password !== repeat
  const mutation = useMutation({
    mutationFn: () => createAdmin(token, username, password),
    onSuccess: () => queryClient.invalidateQueries(),
  })

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        if (!mismatch) mutation.mutate()
      }}
    >
      <Field id="admin-username" label="Username">
        <Input
          id="admin-username"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          autoComplete="username"
          required
        />
      </Field>
      <Field id="admin-password" label="Password" hint="At least 10 characters.">
        <Input
          id="admin-password"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete="new-password"
          minLength={10}
          required
        />
      </Field>
      <Field id="admin-repeat" label="Repeat password">
        <Input
          id="admin-repeat"
          type="password"
          value={repeat}
          onChange={(e) => setRepeat(e.target.value)}
          autoComplete="new-password"
          aria-invalid={mismatch}
          required
        />
      </Field>
      {mismatch && <p className="text-sm text-destructive">The passwords don't match.</p>}
      <FormError error={mutation.error} />
      <FormActions>
        <Button type="submit" disabled={!token || mismatch || mutation.isPending}>
          {mutation.isPending ? "Creating…" : "Create account and open Flowmon"}
        </Button>
      </FormActions>
    </form>
  )
}
