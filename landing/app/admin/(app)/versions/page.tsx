import { hub, type Releases } from '../../../../lib/hub';
import { date, mo, releaseStateLabel } from '../../../../lib/format';
import { MinimumButton, NotesForm, PublishButton } from './forms';

export const metadata = { title: 'Versions' };

const statePill: Record<string, string> = { published: 'ok', pending: 'warn', withdrawn: 'off' };

export default async function VersionsPage() {
  const data = await hub<Releases>('/api/v1/admin/releases?platform=android&channel=website');
  const releases = [...data.releases].sort((a, b) => b.build - a.build);
  const names = new Map(releases.map((r) => [r.build, r.versionName]));
  const phones = data.versions.reduce((n, v) => n + v.installs, 0);
  const minimum = releases.find((r) => r.build === data.minimumBuild);
  const buildLabel = (build: number) => (names.has(build) ? `${names.get(build)} (build ${build})` : `build ${build}`);

  return (
    <>
      <div className="bo-head">
        <h1>Versions de l’application</h1>
        <p className="bo-muted">
          APK Android du site. Une version déclarée par la CI reste en attente jusqu’à sa publication : essayez-la sur
          un téléphone avant de la publier.
        </p>
      </div>

      <section className="bo-stats">
        <div className={`bo-stat ${data.minimumBuild ? 'warn' : ''}`}>
          <span>Version minimale</span>
          <strong>{data.minimumBuild ? (minimum?.versionName ?? `build ${data.minimumBuild}`) : 'aucune'}</strong>
          {data.minimumBuild ? <small className="bo-muted">build {data.minimumBuild}</small> : null}
        </div>
        <div className="bo-stat ok">
          <span>Téléphones vus ({data.windowDays} jours)</span>
          <strong>{phones}</strong>
        </div>
        <div className="bo-stat">
          <span>Versions publiées</span>
          <strong>{releases.filter((r) => r.state === 'published').length}</strong>
        </div>
      </section>

      {data.minimumBuild ? (
        <div className="bo-row">
          <MinimumButton platform={data.platform} channel={data.channel} build={0} />
        </div>
      ) : null}

      <section className="bo-card flush">
        <h2 className="bo-pad">Versions déclarées</h2>
        <div className="bo-table-wrap">
          <table className="bo-table">
            <thead>
              <tr>
                <th>Version</th>
                <th>État</th>
                <th>Dates</th>
                <th>Taille</th>
                <th>Téléphones</th>
                <th>APK</th>
                <th>Notes</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {releases.map((r) => {
                const label = `${r.versionName} (build ${r.build})`;
                const published = r.state === 'published';
                return (
                  <tr key={r.id}>
                    <td>
                      <strong>{r.versionName}</strong>
                      <br />
                      <small className="bo-mono">build {r.build}</small>
                      {r.build === data.minimumBuild ? (
                        <>
                          <br />
                          <span className="bo-pill warn">Minimum</span>
                        </>
                      ) : null}
                    </td>
                    <td>
                      <span className={`bo-pill ${statePill[r.state] ?? ''}`}>{releaseStateLabel[r.state] ?? r.state}</span>
                    </td>
                    <td>
                      <small>Déclarée le {date(r.declaredAt)}</small>
                      {r.publishedAt ? (
                        <>
                          <br />
                          <small>Publiée le {date(r.publishedAt)}</small>
                        </>
                      ) : null}
                      {r.state === 'withdrawn' && r.withdrawnAt ? (
                        <>
                          <br />
                          <small>Retirée le {date(r.withdrawnAt)}</small>
                        </>
                      ) : null}
                    </td>
                    <td>{mo(r.size)}</td>
                    <td>{r.installs}</td>
                    <td>
                      <a href={r.url} rel="noopener noreferrer">
                        Télécharger
                      </a>
                      <br />
                      <small className="bo-mono" title={r.sha256}>
                        sha256 {r.sha256.slice(0, 12)}…
                      </small>
                    </td>
                    <td className="bo-wrap-text">
                      <NotesForm id={r.id} notes={r.notes} />
                    </td>
                    <td>
                      <div className="bo-row">
                        <PublishButton id={r.id} published={published} label={label} />
                        {published && r.build !== data.minimumBuild ? (
                          <MinimumButton platform={data.platform} channel={data.channel} build={r.build} label={label} />
                        ) : null}
                      </div>
                    </td>
                  </tr>
                );
              })}
              {releases.length === 0 ? (
                <tr>
                  <td colSpan={8} className="bo-muted">
                    Aucune version déclarée. Une version arrive ici toute seule quand la CI publie l’APK du site (tag
                    <code>mobile-v*</code>, job « declare »), avec le statut « En attente ». Les boutons « Publier »,
                    « Rendre minimum » et « Notes » apparaissent alors sur sa ligne.
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
      </section>

      <section className="bo-card flush">
        <h2 className="bo-pad">Téléphones par version</h2>
        <div className="bo-table-wrap">
          <table className="bo-table">
            <thead>
              <tr>
                <th>Version</th>
                <th>Téléphones ({data.windowDays} jours)</th>
              </tr>
            </thead>
            <tbody>
              {data.versions.map((v) => (
                <tr key={v.build}>
                  <td>
                    {buildLabel(v.build)}
                    {!names.has(v.build) ? <small> non déclarée</small> : null}
                  </td>
                  <td>{v.installs}</td>
                </tr>
              ))}
              {data.versions.length === 0 ? (
                <tr>
                  <td colSpan={2} className="bo-muted">
                    Aucun téléphone vu sur cette période.
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
      </section>
    </>
  );
}
